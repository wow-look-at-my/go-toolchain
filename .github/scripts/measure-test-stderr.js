// Runs the pipeline's go test command with both pipes read continuously and
// reports how much stderr the child wrote before its first stdout byte.
// Usage: node measure-test-stderr.js <go-toolchain binary> <package>...
const { spawn, execFileSync } = require('child_process');

const [gt, ...pkgs] = process.argv.slice(2);
if (!gt || pkgs.length === 0) throw new Error('usage: measure-test-stderr.js <go-toolchain binary> <package>...');

const cpus = require('os').cpus().length;
const args = ['go', 'test', '-json', '-timeout=5m', '-p', String(cpus), '-parallel', String(cpus),
	'-coverprofile=' + process.env.RUNNER_TEMP + '/measure-cover.out', '-coverpkg=./...', '-count=1', ...pkgs];
console.log('command: ' + gt + ' ' + args.join(' '));

const start = Date.now();
let stderrBytes = 0;
let stdoutBytes = 0;
let stderrAtFirstStdout = -1;
let firstStdoutAt = -1;
const child = spawn(gt, args, { stdio: ['ignore', 'pipe', 'pipe'] });
child.stdout.on('data', (d) => {
	if (stdoutBytes === 0) {
		stderrAtFirstStdout = stderrBytes;
		firstStdoutAt = Date.now() - start;
	}
	stdoutBytes += d.length;
});
child.stderr.on('data', (d) => { stderrBytes += d.length; });
const killer = setTimeout(() => { console.log('measure: killing the child after the bound'); child.kill('SIGKILL'); }, 600000);
child.on('close', (code) => {
	clearTimeout(killer);
	console.log('exit: ' + code + ' after ' + ((Date.now() - start) / 1000).toFixed(1) + 's');
	console.log('stdout bytes total: ' + stdoutBytes);
	console.log('stderr bytes total: ' + stderrBytes);
	console.log('first stdout byte at: ' + (firstStdoutAt < 0 ? 'never' : firstStdoutAt + 'ms'));
	console.log('stderr bytes written before the first stdout byte: ' + (stderrAtFirstStdout < 0 ? stderrBytes + ' (no stdout at all)' : stderrAtFirstStdout));
	console.log('NT anonymous pipe buffer (CreatePipe nSize=0): 4096 bytes');
});

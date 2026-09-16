// Runs every package's tests as its own go test process, bounded, so a package
// that hangs is named instead of hiding behind go test's in-order output.
// Usage: node test-each-package.js <go-toolchain binary>
const { execFileSync, spawnSync } = require('child_process');

const gt = process.argv[2];
if (!gt) throw new Error('usage: test-each-package.js <go-toolchain binary>');

const perPackageSeconds = 240;

function df() {
	console.log('[df ' + new Date().toISOString() + ']');
	console.log(execFileSync('df', ['-h'], { encoding: 'utf8' }));
}

const pkgs = execFileSync(gt, ['go', 'list', './...'], { encoding: 'utf8' })
	.split('\n').filter(Boolean);
console.log('packages: ' + pkgs.length);
df();

const summary = [];
for (const pkg of pkgs) {
	const args = ['go', 'test', '-json', '-timeout=2m', '-count=1', '-coverpkg=./...', pkg];
	console.log('=== ' + new Date().toISOString() + ' start ' + pkg);
	const start = Date.now();
	const res = spawnSync(gt, args, { stdio: 'inherit', timeout: perPackageSeconds * 1000, killSignal: 'SIGKILL' });
	const secs = ((Date.now() - start) / 1000).toFixed(1);
	const outcome = res.error ? 'KILLED (' + res.error.code + ')' : 'exit ' + res.status;
	console.log('=== ' + new Date().toISOString() + ' end ' + pkg + ' ' + outcome + ' ' + secs + 's');
	summary.push(pkg + ' ' + outcome + ' ' + secs + 's');
	df();
}

console.log('--- summary ---');
console.log(summary.join('\n'));

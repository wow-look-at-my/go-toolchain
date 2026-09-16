// Prints free disk space once a minute, for a streamed CI step.
const { execSync } = require('child_process');

function sample() {
	console.log('[df ' + new Date().toISOString() + ']');
	console.log(execSync('df -h', { encoding: 'utf8' }));
}

sample();
setInterval(sample, 60000);

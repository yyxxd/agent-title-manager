// Isolated GitHub Releases fixture for in-app update acceptance tests.
// Serves a signed stable release from a local directory; no real GitHub call.
import http from 'node:http';
import fs from 'node:fs';
import path from 'node:path';

const root = process.argv[2];
const port = Number(process.argv[3] || 18765);
if (!root || !fs.existsSync(root)) throw new Error('fixture directory required');

const manifest = JSON.parse(fs.readFileSync(path.join(root, 'update-manifest.json'), 'utf8'));
const base = `http://127.0.0.1:${port}`;
const release = {
  tag_name: 'v' + manifest.version,
  draft: false,
  prerelease: false,
  body: manifest.notes,
  assets: [
    { name: 'update-manifest.json', size: fs.statSync(path.join(root, 'update-manifest.json')).size, browser_download_url: base + '/update-manifest.json' },
    { name: manifest.file, size: manifest.size, browser_download_url: base + '/' + manifest.file },
  ],
};

http.createServer((req, res) => {
  if (req.url === '/release') {
    res.writeHead(200, { 'content-type': 'application/json' });
    res.end(JSON.stringify(release));
    return;
  }
  if (req.url === '/update-manifest.json') {
    res.writeHead(200, { 'content-type': 'application/json' });
    res.end(fs.readFileSync(path.join(root, 'update-manifest.json')));
    return;
  }
  if (req.url === '/' + manifest.file) {
    const stat = fs.statSync(path.join(root, manifest.file));
    res.writeHead(200, { 'content-type': 'application/octet-stream', 'content-length': stat.size });
    fs.createReadStream(path.join(root, manifest.file)).pipe(res);
    return;
  }
  res.writeHead(404);
  res.end();
}).listen(port, '127.0.0.1', () => console.log(`fixture ready on ${base}`));

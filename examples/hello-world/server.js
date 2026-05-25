const http = require('http');
const port = process.env.PORT || 5000;

http.createServer((req, res) => {
  res.writeHead(200, { 'Content-Type': 'text/plain' });
  res.end(`Hello from StackFly!\nHostname: ${require('os').hostname()}\n`);
}).listen(port, () => {
  console.log(`Listening on port ${port}`);
});

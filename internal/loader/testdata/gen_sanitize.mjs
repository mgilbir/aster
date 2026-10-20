// Records what upstream does with a URI string, for internal/loader's tests:
//
//   sanitize: vega-loader's sanitize (the string Vega writes into a link or
//     an image href, and then loads) for each URI and each set of loader
//     options (baseURL, defaultProtocol);
//   urls: what fetch makes of a string it is given: the WHATWG URL parser
//     (new URL), and for the paths and queries, the request line a server
//     really receives from node's fetch.
//
// Regenerate with the pinned node: NODE_PATH=<root>/testdata/oracle-node/node_modules
// node testdata/gen_sanitize.mjs | gzip -9 -n > testdata/sanitize.json.gz (in internal/loader).
import { createRequire } from 'node:module';
import http from 'node:http';
import path from 'node:path';
const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const vega = await import(require.resolve('vega'));

const uris = [
  // plain, relative, absolute
  'cars.json', 'data/cars.json', './data/cars.json', '../up.json', '/abs.json', 'a/../b', 'a//b', 'a b.json', 'é.json',
  'page.html?q=1#f', '?q', '#frag', 'a?b=c d', '22:48.json', 'a:b', 'A:b', 'x.y:z', 'x+y:z', '-a:b', '1:2',
  'https://example.com/data.json', 'HTTPS://EXAMPLE.COM/Data.json', 'http://example.com/a b', 'http://example.com/a[b]c',
  'http://example.com/a/./b/../c', 'http://example.com/%2e%2e/x', 'http://example.com/a%zz', 'http://example.com/é',
  'http://example.com:80/x', 'http://example.com:8080/x', 'http://Example.COM./x', 'http://[::1]/x', 'http://[::1]:81/x',
  'http://user:pw@example.com/x', 'http://example.com?q=a b', 'http://example.com#f', 'http://example.com',
  'ftp://example.com/x', 'ftps://example.com/x', 'mailto:a@b.example', 'tel:+123', 'callto:x', 'cid:x', 'xmpp:x@y',
  'file:///etc/passwd', 'file://host/x', 'FILE:///x', 'file:x', 'data:text/plain,hi', 'data:image/png;base64,AAAA', 'DATA:text/plain,hi',
  '//cdn.example/x.json', '//cdn.example', '///x', '//', 'http://', 'https:', 'http:example.com/x', 'http:/example.com/x',
  'http:\\\\example.com\\x', 'http://example.com\\@evil.example/x', 'http://example.com/\tx\ny\rz',
  ' https://example.com/x', 'https://example.com/x ', '\thttps://example.com/x', '\u00a0https://example.com/x',
  'h t t p://example.com', 'java\tscript:alert(1)', 'javascript:alert(1)', ' JaVaScRiPt:x', 'vbscript:x', '\u0000javascript:x',
  'javascript\u2028:x', '\u212a:x', 'K:x', 'ab:cd', 'ab', 'a-b+c.d', 'a-b+c.d:e', '', ' ', '\t', '\u3000', 'mailto', 'mailto:',
  'http://0x7f.1/x', 'http://2130706433/x', 'http://127.1/x', 'http://[::ffff:7f00:1]/x', 'http://localhost/x',
  'https://example.com//x', 'https://example.com/x//', '/', '//example.com/', 'http:///example.com/x',
];
const optionSets = [
  {},
  { baseURL: 'https://h.example/data/' },
  { baseURL: 'https://h.example/data' },
  { baseURL: 'https://h.example' },
  { baseURL: '/rel/' },
  { baseURL: 'rel' },
  { baseURL: '//cdn.example/x' },
  { baseURL: 'file:///srv/' },
  { defaultProtocol: 'https' },
  { defaultProtocol: 'file' },
  { baseURL: 'https://h.example/', defaultProtocol: 'https' },
];

const sanitize = [];
for (const options of optionSets) {
  const l = vega.loader();
  for (const uri of uris) {
    let href = null;
    try {
      href = (await l.sanitize(uri, options)).href;
    } catch (e) { /* refused */ }
    sanitize.push({ uri, options, href });
  }
}

// Strings handed to fetch. Each is parsed as new URL(s) does; the hostile
// ones are the spellings that differ between WHATWG and Go's net/url.
const urls = [
  'http://example.com/', 'HTTP://EXAMPLE.COM/Path', 'https://example.com:443/x', 'http://example.com:80/x', 'http://example.com:81/x',
  'http://example.com:0080/x', 'http://example.com:/x', 'http://example.com:65535/x', 'http://example.com:65536/x', 'http://example.com:x/x',
  'http://example.com./', 'http://EXAMPLE.com../', 'http://a_b.example/', 'http://a!b.example/', 'http://a$b.example/', 'http://a,b.example/',
  'http://example.com', 'http://example.com?q', 'http://example.com#f', 'http://example.com?', 'http://example.com/?', 'http://example.com/#',
  'http://example.com/a/./b/../c', 'http://example.com/a/b/..', 'http://example.com/a/b/.', 'http://example.com/..', 'http://example.com/../../x',
  'http://example.com/%2e%2e/x', 'http://example.com/%2E/x', 'http://example.com/.%2e/x', 'http://example.com/%2e./x', 'http://example.com/a/%2e%2e',
  'http://example.com/a%2fb', 'http://example.com/a%zz', 'http://example.com/a%', 'http://example.com/a%2', 'http://example.com/a%41',
  'http://example.com/a b', 'http://example.com/a"b<c>d`e{f}g^h|i', 'http://example.com/a[b]c', 'http://example.com/é', 'http://example.com/\u{1F600}',
  'http://example.com/\ud800', 'http://example.com/a\\b', 'http://example.com/a\\..\\b', 'http://example.com//a//b',
  'http://example.com/?a b"c#d', 'http://example.com/?a\'b<c>d`e{f}', 'http://example.com/?é', 'http://example.com/?a\\b', 'http://example.com/?a%zz',
  'http://example.com/\tx\ny\rz', 'http://exa\tmple.com/', 'http://exa\nmple.com/', 'http://example\r.com/', 'ht\ttp://example.com/', ' http://example.com/', 'http://example.com/ ', '\u0001http://example.com/\u0001', '\u00a0http://example.com/',
  'http://example.com\\@evil.example/x', 'http://example.com\\x', 'http:\\\\example.com\\x', 'http:/\\example.com/', 'http:example.com/x', 'http:/example.com/x', 'http:///example.com/x', 'http:////example.com/x', 'http:',
  'http://', 'http:///', 'http://:80/', 'http://?', 'http://#', 'http:// /', 'http://a b/',
  'http://user@example.com/', 'http://user:pw@example.com/', 'http://@example.com/', 'http://:@example.com/', 'http://a@b@example.com/', 'http://example.com%40evil.example/', 'http://evil.example#@example.com/', 'http://evil.example?@example.com/', 'http://evil.example/@example.com',
  'http://example.com%2eevil.example/', 'http://example%2ecom/', 'http://EXAMPLE%2Ecom/', 'http://example.com%00/', 'http://example.com\u0000/', 'http://exam\u0000ple.com/', 'http://example.com%0a/', 'http://example.com%20/', 'http://exam ple.com/', 'http://example.com%25/', 'http://%41.example/', 'http://%/', 'http://%zz/',
  'http://exa<mple.com/', 'http://exa>mple.com/', 'http://exa^mple.com/', 'http://exa|mple.com/', 'http://exa[mple.com/', 'http://exa]mple.com/', 'http://exa"mple.com/', "http://exa'mple.com/", 'http://exa`mple.com/', 'http://exa{mple.com/', 'http://exa}mple.com/', 'http://exa~mple.com/', 'http://exa*mple.com/', 'http://exa;mple.com/', 'http://exa=mple.com/', 'http://exa&mple.com/', 'http://exa+mple.com/', 'http://exa(mple.com/', 'http://exa)mple.com/',
  'http://bücher.de/', 'http://xn--bcher-kva.de/', 'http://XN--BCHER-KVA.de/', 'http://xn--/', 'http://xn--a/', 'http://ａllowed.com/', 'http://allowed。com/', 'http://allowed．com/', 'http://\u212aali.org/', 'http://a\u00adb.example/', 'http://a\u200bb.example/', 'http://%C3%BCber.example/', 'http://ß.example/',
  'http://127.0.0.1/', 'http://127.1/', 'http://127.0.1/', 'http://0x7f.1/', 'http://0x7f000001/', 'http://2130706433/', 'http://017700000001/', 'http://0177.0.0.1/', 'http://0177.0.0.01/', 'http://127.0.0.1./', 'http://127.0.0.1../', 'http://1.2.3.4.5/', 'http://256.1.1.1/', 'http://1.2.3.256/', 'http://1.2.3/', 'http://4294967295/', 'http://4294967296/', 'http://0/', 'http://00/', 'http://0x/', 'http://0X7F.0.0.1/', 'http://09/', 'http://0.0.0.09/', 'http://1.2.3.4a/', 'http://a.1/', 'http://a.0x1/', 'http://1.a/', 'http://1.2.3.0x/', 'http://999999999999999999999/', 'http://10/', 'http://08.1/',
  'http://[::1]/', 'http://[::1]:8080/', 'http://[::]/', 'http://[0:0:0:0:0:0:0:1]/', 'http://[::ffff:127.0.0.1]/', 'http://[::ffff:7f00:1]/', 'http://[::127.0.0.1]/', 'http://[2001:db8::1]/', 'http://[2001:DB8::A]/', 'http://[2001:db8:0:0:1:0:0:1]/', 'http://[1:0:0:2:0:0:0:3]/', 'http://[fe80::1%25eth0]/', 'http://[fe80::1%eth0]/', 'http://[::1/', 'http://::1/', 'http://[::1]x/', 'http://[::1]:x/', 'http://[]/', 'http://[:::1]/', 'http://[1::2::3]/', 'http://[0::1.2.3.4]/', 'http://[::1.2.3.04]/', 'http://[1:2:3:4:5:6:7:8]/', 'http://[1:2:3:4:5:6:7:8:9]/', 'http://[12345::]/', 'http://[::g]/', 'http://[64:ff9b::7f00:1]/', 'http://[2002:7f00:1::]/',
  'ftp://example.com/', 'file:///etc/passwd', 'mailto:a@b', 'data:text/plain,hi', 'javascript:alert(1)', 'blob:http://example.com/x', 'ws://example.com/', 'example.com/x', '/x', 'x', '', ' ', '//example.com/', '?q', '#f', 'a:b', '1http://example.com/', 'h ttp://example.com/', 'http//example.com/', 'http:/', 'https://example.com',
];
const out = [];
for (const s of urls) {
  const rec = { in: s };
  try {
    const u = new URL(s);
    if (u.protocol !== 'http:' && u.protocol !== 'https:') {
      rec.kind = 'notHTTP';
    } else {
      rec.kind = u.username || u.password ? 'userinfo' : 'ok';
      u.hash = '';
      rec.href = u.href;
      rec.host = u.host;
      rec.pathq = u.href.slice(u.origin.length); // path and query as serialized, a bare ? included
    }
  } catch (e) {
    rec.kind = 'invalid';
  }
  out.push(rec);
}

// What fetch sends for a path and a query: the request line a server receives.
const lines = new Map();
const server = http.createServer((req, res) => {
  lines.set(req.headers['x-case'], { url: req.url, host: req.headers.host });
  res.end('ok');
});
await new Promise((r) => server.listen(0, '127.0.0.1', r));
const port = server.address().port;
for (const rec of out) {
  if (rec.kind !== 'ok') continue;
  // Only requests that can reach the local server: rewrite the host.
  const u = new URL(rec.href);
  u.protocol = 'http:';
  u.hostname = '127.0.0.1';
  u.port = String(port);
  const key = String(lines.size) + ':' + rec.in;
  try {
    await fetch(u.href, { headers: { 'x-case': key } });
  } catch (e) { /* fetch refused (a bad port, say) */ }
  const got = lines.get(key);
  if (got) {
    rec.wire = got.url;
    if (got.url !== rec.pathq) throw new Error('wire differs from url for ' + JSON.stringify(rec.in));
  }
}
server.close();

console.log(JSON.stringify({ node: process.version, vegaLoader: '5.1.3', sanitize, urls: out }, null, 0).replace(/\},\{/g, '},\n{'));

#!/usr/bin/env node
// Opt-in smoke test. Never prints headers, raw bodies, credentials, or errors.
// Search/extract require a separately verified FREE key quota; this flag is an
// operator attestation, not a billing API check. No anonymous search fallback.
const args = process.argv.slice(2);
const option = (name, fallback) => {
  const index = args.indexOf(name);
  return index === -1 ? fallback : args[index + 1];
};
const operation = option('--operation', 'search');
const key = process.env.ANYSEARCH_API_KEY?.trim();
const confirmFree = args.includes('--confirm-free-quota');
if (!['search', 'sub-domains', 'extract'].includes(operation)) {
  console.error('Invalid operation. Use search, sub-domains, or extract.');
  process.exit(2);
}
if (!key) {
  console.error('Blocked: securely configure ANYSEARCH_API_KEY in the process environment.');
  process.exit(2);
}
if (operation !== 'sub-domains' && !confirmFree) {
  console.error('Blocked: independently verify free key quota and disabled paid billing, then pass --confirm-free-quota.');
  process.exit(2);
}
const headers = { Authorization: `Bearer ${key}`, Accept: 'application/json' };
let endpoint = `https://api.anysearch.com/v1/${operation}`;
const request = { headers, signal: AbortSignal.timeout(30000), redirect: 'error' };
if (operation === 'sub-domains') {
  endpoint += '?domain=code';
} else {
  request.method = 'POST';
  headers['Content-Type'] = 'application/json';
  const payload = operation === 'search'
    ? { query: 'RAGFlow open source retrieval augmented generation', max_results: 6 }
    : { url: 'https://www.python.org/about/' };
  request.body = JSON.stringify(payload);
}
try {
  const response = await fetch(endpoint, request);
  if (!response.ok) throw new Error('request failed');
  const reader = response.body.getReader();
  let size = 0;
  const chunks = [];
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    size += value.byteLength;
    if (size > 4 * 1024 * 1024) { await reader.cancel(); throw new Error('oversize'); }
    chunks.push(value);
  }
  const result = JSON.parse(Buffer.concat(chunks).toString('utf8'));
  if (result?.code !== 0 || !result.data || typeof result.data !== 'object' || Array.isArray(result.data)) throw new Error('invalid envelope');
  let count;
  if (operation === 'search') {
    if (!Array.isArray(result.data.results)) throw new Error('invalid results');
    count = result.data.results.filter((item) => {
      try {
        const url = new URL(item.url);
        const text = typeof item.content === 'string' && item.content.trim() ? item.content : item.snippet;
        return ['https:', 'http:'].includes(url.protocol) && !url.username && !url.password && typeof text === 'string' && text.trim();
      } catch { return false; }
    }).length;
    if (!count) throw new Error('no usable results');
  } else if (operation === 'sub-domains') {
    if (!Array.isArray(result.data.domains)) throw new Error('invalid domains');
    count = result.data.domains.length;
  } else {
    if (typeof result.data.content !== 'string' || !result.data.content.trim()) throw new Error('no content');
    count = result.data.content.length;
  }
  console.log(JSON.stringify({ operation, authenticated: true, http_status: response.status, code: 0, count }));
} catch {
  console.error('AnySearch live check failed. Raw response and exception details are suppressed.');
  process.exitCode = 1;
}

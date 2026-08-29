// Extract the verifier from the shipped page and run it against real data, so the
// browser implementation is tested without a browser.
import { readFileSync } from 'fs';
const html = readFileSync(process.argv[2], 'utf8');
const src = html.split('<script>').pop().split('</script>')[0];
const body = src.slice(src.indexOf('function canonical'), src.indexOf('/* ---- history'));
const { canonical, verifyChain } = await import('data:text/javascript,' +
  encodeURIComponent(body + '\nexport { canonical, verifyChain };'));

// 1. Golden vector from the Go test suite.
const core = { trace_uuid:'8f14e45f-ceea-467a-9c3b-1f2a4d5e6b70', payload:'hello world',
  sequence:['us-west1','us-central1','us-east4','europe-west1','europe-central2','asia-northeast1'],
  created_at:'2026-08-29T19:04:11.221Z' };
const d = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(canonical(core)));
const got = [...new Uint8Array(d)].map(b=>b.toString(16).padStart(2,'0')).join('');
const want = '55fc7498426a6500068d2fa6f43ac311c7dcfff4ec43e5b1940ebd74de0f0049';
console.log(`genesis golden vector: ${got === want ? 'MATCH' : 'MISMATCH\n  got  '+got+'\n  want '+want}`);

// 2. A real ring produced by the Go implementation.
const env = JSON.parse(readFileSync(process.argv[3], 'utf8'));
const v = await verifyChain(env);
console.log(`real ring (${env.receipts.length} receipts): ${v.ok ? 'VERIFIED' : 'BROKEN — '+v.detail}`);

// 3. Tamper: the browser must reject an altered receipt.
const t = JSON.parse(JSON.stringify(env));
t.receipts[3].region = 'us-east4';
const tv = await verifyChain(t);
console.log(`tampered ring: ${tv.ok ? 'NOT DETECTED (bug)' : 'rejected — ' + tv.detail}`);
process.exit(got === want && v.ok && !tv.ok ? 0 : 1);

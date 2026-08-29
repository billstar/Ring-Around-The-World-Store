import * as ff from '@google-cloud/functions-framework';
import { readFileSync } from 'fs';
import { join } from 'path';
import { queryTraces, validateUuids } from './logs';

// The origin URL is injected at deploy time and served to the browser at runtime.
// It is deliberately never written into a tracked file.
const ORIGIN_URL = process.env.RATW_ORIGIN_URL ?? '';

const page = () => readFileSync(join(__dirname, '..', 'public', 'index.html'), 'utf8');

ff.http('Web', async (req: ff.Request, res: ff.Response) => {
  const path = (req.path || '/').replace(/\/+$/, '') || '/';

  if (path === '/config.js') {
    res.type('application/javascript').send(
      `window.RATW_CONFIG = ${JSON.stringify({ originUrl: ORIGIN_URL })};`
    );
    return;
  }

  if (path === '/logs') {
    if (req.method !== 'POST') {
      res.status(405).send('POST only');
      return;
    }
    try {
      const uuids = validateUuids(req.body?.trace_uuids);
      const traces = await queryTraces(uuids);
      res.json({ traces });
    } catch (err: any) {
      // Validation failures are the client's fault and never reach the log filter.
      res.status(400).json({ error: String(err?.message ?? err) });
    }
    return;
  }

  if (path === '/' || path === '/index.html') {
    res.type('text/html').send(page());
    return;
  }
  res.status(404).send('not found');
});

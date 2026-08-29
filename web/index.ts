import * as ff from '@google-cloud/functions-framework';
import { readFileSync } from 'fs';
import { join } from 'path';
import { ownedTraces, queryTraces, validateClientId, validateUuids } from './logs';

// The origin URL is injected at deploy time and served to the browser at runtime.
// It is deliberately never written into a tracked file.
const ORIGIN_URL = process.env.RATW_ORIGIN_URL ?? '';

// Config is injected INTO the html at serve time rather than fetched as a second
// request. A separate /config.js breaks whenever the app is mounted under a path
// prefix (the legacy cloudfunctions.net alias serves it at /ratw-web), and the page
// then silently renders with no origin configured.
const page = () => {
  const html = readFileSync(join(__dirname, '..', 'public', 'index.html'), 'utf8');
  const inject = `<script>window.RATW_CONFIG = ${JSON.stringify({ originUrl: ORIGIN_URL })};</script>`;
  return html.replace('<!--RATW_CONFIG-->', inject);
};

ff.http('Web', async (req: ff.Request, res: ff.Response) => {
  // The legacy cloudfunctions.net endpoint mounts the function under /ratw-web;
  // the run.app endpoint serves it at the root. Accept both.
  const path = (req.path || '/').replace(/^\/ratw-web/, '').replace(/\/+$/, '') || '/';

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
      const clientId = validateClientId(req.body?.client_id);
      // Only rings this client actually started are queryable, even if it asks for
      // a uuid it obtained some other way.
      const owned = await ownedTraces(clientId, uuids);
      const traces = await queryTraces([...owned]);
      res.json({ traces, requested: uuids.length, owned: owned.size });
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

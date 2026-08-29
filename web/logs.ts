import { Logging } from '@google-cloud/logging';

const logging = new Logging();

// Strict UUID shape. Everything that reaches the Cloud Logging filter passes this
// first (FR-10.3): a filter is an injectable query language and is treated with the
// same suspicion as SQL.
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const MAX_UUIDS = 100;

export interface HopEvent {
  hop_index: number;
  region: string;
  stage: string;
  duration_ms?: number;
  link_hash?: string;
  object?: string;
  generation?: number;
  error?: string;
  severity: string;
  timestamp: string;
}

export interface TraceView {
  complete: boolean;
  failed: boolean;
  total_ms?: number;
  events: HopEvent[];
}

export function validateUuids(input: unknown): string[] {
  if (!Array.isArray(input)) throw new Error('trace_uuids must be an array');
  if (input.length > MAX_UUIDS) throw new Error(`at most ${MAX_UUIDS} trace_uuids`);
  const out: string[] = [];
  for (const v of input) {
    if (typeof v !== 'string' || !UUID_RE.test(v)) throw new Error(`invalid trace uuid: ${String(v).slice(0, 64)}`);
    out.push(v);
  }
  return out;
}

export async function queryTraces(uuids: string[]): Promise<Record<string, TraceView>> {
  const result: Record<string, TraceView> = {};
  for (const u of uuids) result[u] = { complete: false, failed: false, events: [] };
  if (uuids.length === 0) return result;

  // The 24h bound is not cosmetic: an unbounded filter scans far more and is markedly
  // slower. Values are already regex-validated, so quoting them is safe.
  const list = uuids.map((u) => `"${u}"`).join(' OR ');
  const filter = [
    'resource.type="cloud_run_revision"',
    `jsonPayload.trace_uuid=(${list})`,
    `timestamp >= "${new Date(Date.now() - 24 * 3600 * 1000).toISOString()}"`,
  ].join(' AND ');

  const [entries] = await logging.getEntries({
    filter,
    orderBy: 'timestamp desc',
    pageSize: 1000,
  });

  for (const entry of entries) {
    const p: any = entry.data ?? {};
    const uuid = p.trace_uuid;
    if (!uuid || !result[uuid]) continue;
    const view = result[uuid];
    view.events.push({
      hop_index: p.hop_index ?? 0,
      region: p.region ?? '',
      stage: p.stage ?? '',
      duration_ms: p.duration_ms,
      link_hash: p.link_hash,
      object: p.object,
      generation: p.generation,
      error: p.error,
      severity: String(entry.metadata?.severity ?? 'INFO'),
      timestamp: String(entry.metadata?.timestamp ?? ''),
    });
    if (p.stage === 'complete') {
      view.complete = true;
      view.total_ms = p.duration_ms;
    }
    if (String(entry.metadata?.severity) === 'ERROR') view.failed = true;
  }

  for (const view of Object.values(result)) {
    view.events.sort((a, b) => a.hop_index - b.hop_index || a.timestamp.localeCompare(b.timestamp));
  }
  return result;
}

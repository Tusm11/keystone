/**
 * keystone-edge: the hot-path redirect Worker.
 *
 * Two-tier read path:
 *   1. Workers KV (edge-local, this PoP)        — serves hits in ~5–15ms
 *   2. Origin API over HTTP                     — serves misses, 20–200ms
 *
 * Write path (POST /shorten):
 *   edge → origin → return response
 *       → populate KV via ctx.waitUntil (non-blocking)
 *
 * No cache invalidation here because codes are immutable in v0.1 — once a
 * code → long_url mapping exists, it never changes. A write-only lifetime
 * means we never have to flush KV, which would otherwise be the hard part.
 */

import { Hono } from 'hono';

type Bindings = {
  ORIGIN_URL: string;
  LINKS: KVNamespace;
};

// Cache TTL at the edge. 1 hour balances "most short links are hot for
// hours, not days" against "a mistake in origin shouldn't live forever."
// 60s minimum is a Workers KV limit on cacheTtl.
const EDGE_CACHE_TTL_SEC = 60 * 60;

const app = new Hono<{ Bindings: Bindings }>();

app.get('/health', (c) =>
  c.json({ status: 'ok', service: 'keystone-edge' })
);

// POST /shorten — forward to origin, pre-warm KV on success.
app.post('/shorten', async (c) => {
  const body = await c.req.json().catch(() => null);
  if (!body || typeof body.url !== 'string' || body.url === '') {
    return c.json({ error: 'url is required' }, 400);
  }

  const originRes = await fetch(`${c.env.ORIGIN_URL}/shorten`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ url: body.url }),
  });

  if (!originRes.ok) {
    const text = await originRes.text();
    return c.json(
      { error: 'origin rejected request', detail: text },
      originRes.status === 400 ? 400 : 502
    );
  }

  const data = (await originRes.json()) as { code: string; long_url: string };

  // Pre-warm the edge cache so the first click of this code is a KV hit,
  // not a miss. ctx.waitUntil keeps the write going even after we return.
  c.executionCtx.waitUntil(
    c.env.LINKS.put(data.code, data.long_url, { expirationTtl: EDGE_CACHE_TTL_SEC })
  );

  const url = new URL(c.req.url);
  const shortUrl = `${url.protocol}//${url.host}/${data.code}`;

  return c.json({ code: data.code, short_url: shortUrl, long_url: data.long_url }, 201);
});

// GET /:code — the hot read path. KV first; origin on miss; populate on miss.
app.get('/:code', async (c) => {
  const code = c.req.param('code');

  // 1. Edge-local cache. cacheTtl keeps the mapping hot in the colo's
  //    local cache for the TTL window, dodging even the KV disk read.
  const cached = await c.env.LINKS.get(code, { cacheTtl: EDGE_CACHE_TTL_SEC });
  if (cached) {
    return c.redirect(cached, 302);
  }

  // 2. Miss → ask origin. Origin itself has a Redis cache in front of
  //    Postgres, so misses are still cheap most of the time.
  const originRes = await fetch(`${c.env.ORIGIN_URL}/${encodeURIComponent(code)}`);

  if (originRes.status === 404) {
    return c.json({ error: 'not found', code }, 404);
  }
  if (!originRes.ok) {
    return c.json({ error: 'origin lookup failed' }, 502);
  }

  const data = (await originRes.json()) as { code: string; long_url: string };

  // 3. Populate KV for next time. waitUntil lets us return the redirect
  //    before the KV write completes — the user doesn't wait on cache fill.
  c.executionCtx.waitUntil(
    c.env.LINKS.put(code, data.long_url, { expirationTtl: EDGE_CACHE_TTL_SEC })
  );

  // 302 (Found) rather than 301 (Moved Permanently): 301 is aggressively
  // browser-cached and would break both click analytics and any future
  // destination change.
  return c.redirect(data.long_url, 302);
});

app.notFound((c) => c.json({ error: 'route not found' }, 404));

export default app;

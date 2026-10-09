/**
 * keystone-edge: the hot-path redirect Worker.
 *
 * Two-tier read path:
 *   1. Workers KV (edge-local, this PoP)  — ~5–15ms
 *   2. Origin API (http)                   — misses only
 *
 * Capability tokens:
 *   - GET /:code?k=<token> — token passes through to origin for verification.
 *     If present, we bypass the edge KV cache: the cache entry is only
 *     code→long_url, which doesn't know whether a token is required for
 *     this request. Correctness beats latency on signed paths; the vast
 *     majority of requests are plain and still hit KV.
 *   - POST /capabilities — passthrough to origin's minting endpoint.
 */

import { Hono } from 'hono';

type Bindings = {
  ORIGIN_URL: string;
  LINKS: KVNamespace;
};

const EDGE_CACHE_TTL_SEC = 60 * 60;

const app = new Hono<{ Bindings: Bindings }>();

app.get('/health', (c) => c.json({ status: 'ok', service: 'keystone-edge' }));

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

  c.executionCtx.waitUntil(
    c.env.LINKS.put(data.code, data.long_url, { expirationTtl: EDGE_CACHE_TTL_SEC })
  );

  const url = new URL(c.req.url);
  const shortUrl = `${url.protocol}//${url.host}/${data.code}`;
  return c.json({ code: data.code, short_url: shortUrl, long_url: data.long_url }, 201);
});

// POST /capabilities — passthrough to origin's minting endpoint.
app.post('/capabilities', async (c) => {
  const rawBody = await c.req.text();
  const originRes = await fetch(`${c.env.ORIGIN_URL}/capabilities`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: rawBody,
  });
  const text = await originRes.text();
  const minted = text ? (JSON.parse(text) as { code: string; token: string }) : null;

  // If origin minted successfully, enrich the response with a ready-to-use
  // capability URL anchored on the edge's own host.
  if (originRes.status === 201 && minted) {
    const url = new URL(c.req.url);
    const capUrl = `${url.protocol}//${url.host}/${minted.code}?k=${encodeURIComponent(minted.token)}`;
    return c.json({ ...minted, capability_url: capUrl }, 201);
  }
  return new Response(text, {
    status: originRes.status,
    headers: { 'Content-Type': 'application/json' },
  });
});

// GET /:code — hot read path; capability token (?k=) bypasses edge cache.
app.get('/:code', async (c) => {
  const code = c.req.param('code');
  const token = c.req.query('k');

  // Signed path: send to origin for verification. Can't trust KV here
  // because the cache doesn't model per-request authorization.
  if (token) {
    const originRes = await fetch(
      `${c.env.ORIGIN_URL}/${encodeURIComponent(code)}?k=${encodeURIComponent(token)}`
    );
    if (originRes.status === 403) {
      return c.json({ error: 'capability rejected' }, 403);
    }
    if (originRes.status === 404) {
      return c.json({ error: 'not found', code }, 404);
    }
    if (!originRes.ok) {
      return c.json({ error: 'origin lookup failed' }, 502);
    }
    const data = (await originRes.json()) as { long_url: string };
    return c.redirect(data.long_url, 302);
  }

  // Unsigned path: edge KV first, origin on miss, populate on miss.
  const cached = await c.env.LINKS.get(code, { cacheTtl: EDGE_CACHE_TTL_SEC });
  if (cached) return c.redirect(cached, 302);

  const originRes = await fetch(`${c.env.ORIGIN_URL}/${encodeURIComponent(code)}`);
  if (originRes.status === 404) return c.json({ error: 'not found', code }, 404);
  if (!originRes.ok) return c.json({ error: 'origin lookup failed' }, 502);

  const data = (await originRes.json()) as { code: string; long_url: string };
  c.executionCtx.waitUntil(
    c.env.LINKS.put(code, data.long_url, { expirationTtl: EDGE_CACHE_TTL_SEC })
  );
  return c.redirect(data.long_url, 302);
});

app.notFound((c) => c.json({ error: 'route not found' }, 404));

export default app;

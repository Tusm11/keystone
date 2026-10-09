/**
 * keystone-edge: the hot-path redirect Worker.
 *
 * - POST /shorten  → forwards to origin, returns the full short URL
 * - GET  /:code    → looks up at origin, returns 302 redirect to the long URL
 * - GET  /health   → cheap liveness check
 *
 * No cache yet; every request falls through to the origin. KV + stampede
 * defenses come in the next phase.
 */

import { Hono } from 'hono';

type Bindings = {
  ORIGIN_URL: string;
};

const app = new Hono<{ Bindings: Bindings }>();

app.get('/health', (c) =>
  c.json({ status: 'ok', service: 'keystone-edge' })
);

// POST /shorten — forward the body to origin, return a usable short URL.
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

  // Build the public short URL from the request's own host so dev and prod
  // just work: localhost:8787 locally, keystone-edge.workers.dev deployed.
  const url = new URL(c.req.url);
  const shortUrl = `${url.protocol}//${url.host}/${data.code}`;

  return c.json({ code: data.code, short_url: shortUrl, long_url: data.long_url }, 201);
});

// GET /:code — resolve at origin, 302 to the long URL.
app.get('/:code', async (c) => {
  const code = c.req.param('code');

  const originRes = await fetch(`${c.env.ORIGIN_URL}/${encodeURIComponent(code)}`);

  if (originRes.status === 404) {
    return c.json({ error: 'not found', code }, 404);
  }
  if (!originRes.ok) {
    return c.json({ error: 'origin lookup failed' }, 502);
  }

  const data = (await originRes.json()) as { code: string; long_url: string };

  // 302 (Found) rather than 301 (Moved Permanently): 301 is cached by
  // browsers aggressively, which breaks analytics and makes it impossible
  // to ever change a code's destination.
  return c.redirect(data.long_url, 302);
});

app.notFound((c) => c.json({ error: 'route not found' }, 404));

export default app;

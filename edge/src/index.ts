/**
 * Welcome to Cloudflare Workers! This is your first worker.
 *
 * - Run `npm run dev` in your terminal to start a development server
 * - Open a browser tab at http://localhost:8787/ to see your worker in action
 * - Run `npm run deploy` to publish your worker
 *
 * Bind resources to your worker in `wrangler.jsonc`. After adding bindings, a type definition for the
 * `Env` object can be regenerated with `npm run cf-typegen`.
 *
 * Learn more at https://developers.cloudflare.com/workers/
 */

import { Hono } from 'hono';

type Bindings = {
  // Will be filled in later — KV, Queues, env vars.
};

const app = new Hono<{ Bindings: Bindings }>();

app.get('/health', (c) => c.json({ status: 'ok', service: 'keystone-edge' }));

app.post('/shorten', async (c) => {
  return c.json({ error: 'not implemented yet' }, 501);
});

app.get('/:code', async (c) => {
  const code = c.req.param('code');
  return c.json({ error: 'not implemented yet', code }, 501);
});

app.notFound((c) => c.json({ error: 'route not found' }, 404));

export default app;

// read-warm: hot-path baseline.
//
// Measures: edge (default) or origin-direct (BASE_URL=...:8080) under
// 100 VUs for 30s. Both targets exercise the same code path through
// Redis + Postgres; the difference is whether the edge Worker wraps it.
//
// Local-dev p99 reference (your laptop may differ):
//   via edge   (wrangler dev + Docker net):  ~500–800ms
//   via origin (direct):                     ~30–80ms
import http from 'k6/http';
import { check } from 'k6';
import { BASE_URL, EXPECT_STATUS, isOkResolve, mintCodes } from './helpers.js';

const N = parseInt(__ENV.N || '300', 10);

export const options = {
  setupTimeout: '10m',
  scenarios: {
    warmup: {
      executor: 'shared-iterations',
      vus: 20,
      iterations: N,
      maxDuration: '2m',
      exec: 'warmup',
      tags: { phase: 'warmup' },
    },
    measure: {
      executor: 'constant-vus',
      vus: 100,
      duration: '30s',
      exec: 'measure',
      tags: { phase: 'measure' },
      startTime: '15s',
    },
  },
  thresholds: {
    'http_req_duration{phase:measure}': ['p(95)<1500', 'p(99)<2500'],
    checks: ['rate>0.99'],
  },
};

export function setup() {
  const codes = mintCodes(http, N, 'warm');
  console.log(`seeded ${codes.length} codes; expecting status ${EXPECT_STATUS} on resolve`);
  return codes;
}

function resolve(code) {
  const res = http.get(`${BASE_URL}/${code}`, { redirects: 0 });
  check(res, { 'resolve ok': (r) => isOkResolve(r) });
}

export function warmup(codes) {
  resolve(codes[__ITER % codes.length]);
}

export function measure(codes) {
  resolve(codes[Math.floor(Math.random() * codes.length)]);
}

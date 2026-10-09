// read-cold: every request is a fresh code; edge+Redis miss, Postgres hit.
import http from 'k6/http';
import { check } from 'k6';
import { BASE_URL, EXPECT_STATUS, isOkResolve, mintCodes } from './helpers.js';

const N = parseInt(__ENV.N || '500', 10);

export const options = {
  setupTimeout: '10m',
  scenarios: {
    cold: {
      executor: 'per-vu-iterations',
      vus: 50,
      iterations: 10,
      maxDuration: '2m',
    },
  },
  thresholds: {
    checks: ['rate>0.99'],
  },
};

export function setup() {
  const codes = mintCodes(http, N, 'cold');
  console.log(`seeded ${codes.length} codes; expecting status ${EXPECT_STATUS} on resolve`);
  return codes;
}

export default function (codes) {
  const perVU = Math.floor(codes.length / 50);
  const idx = ((__VU - 1) * perVU + __ITER) % codes.length;
  const res = http.get(`${BASE_URL}/${codes[idx]}`, { redirects: 0 });
  check(res, { 'resolve ok': (r) => isOkResolve(r) });
}

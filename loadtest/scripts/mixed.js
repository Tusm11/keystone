// mixed: 95% read / 5% write at 300 req/s for 45s.
import http from 'k6/http';
import { check } from 'k6';
import { BASE_URL, EXPECT_STATUS, isOkResolve, mintCodes } from './helpers.js';

const N = parseInt(__ENV.N || '300', 10);

export const options = {
  setupTimeout: '10m',
  scenarios: {
    traffic: {
      executor: 'constant-arrival-rate',
      rate: 300,
      timeUnit: '1s',
      duration: '45s',
      preAllocatedVUs: 100,
      maxVUs: 300,
    },
  },
  thresholds: {
    'http_req_duration{op:read}':  ['p(95)<1500', 'p(99)<3000'],
    'http_req_duration{op:write}': ['p(95)<2000', 'p(99)<4000'],
    checks: ['rate>0.95'],
  },
};

export function setup() {
  const codes = mintCodes(http, N, 'mixed');
  console.log(`seeded ${codes.length} codes; expecting status ${EXPECT_STATUS} on resolve`);
  return codes;
}

export default function (codes) {
  if (Math.random() < 0.95) {
    const code = codes[Math.floor(Math.random() * codes.length)];
    const res = http.get(`${BASE_URL}/${code}`, { redirects: 0, tags: { op: 'read' } });
    check(res, { 'read ok': (r) => isOkResolve(r) });
  } else {
    const res = http.post(
      `${BASE_URL}/shorten`,
      JSON.stringify({ url: `https://example.com/mixed/${Date.now()}/${Math.random()}` }),
      { headers: { 'Content-Type': 'application/json' }, tags: { op: 'write' } }
    );
    check(res, { 'write 201': (r) => r.status === 201 });
  }
}

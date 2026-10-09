// stampede: 200 VUs on one fresh code — prove the singleflight defense.
import http from 'k6/http';
import { check } from 'k6';
import { BASE_URL, EXPECT_STATUS, isOkResolve, mintCodes } from './helpers.js';

export const options = {
  setupTimeout: '2m',
  scenarios: {
    thundering_herd: {
      executor: 'per-vu-iterations',
      vus: 200,
      iterations: 1,
      maxDuration: '30s',
      gracefulStop: '5s',
    },
  },
  thresholds: {
    checks: ['rate==1.0'],
    http_req_failed: ['rate==0'],
  },
};

export function setup() {
  const codes = mintCodes(http, 1, 'stampede');
  console.log(`stampede target: ${codes[0]}; expecting status ${EXPECT_STATUS} on resolve`);
  return codes[0];
}

export default function (code) {
  const res = http.get(`${BASE_URL}/${code}`, { redirects: 0 });
  check(res, { 'resolve ok': (r) => isOkResolve(r) });
}

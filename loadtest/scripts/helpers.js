// Shared helpers for all k6 scenarios.

export const BASE_URL = __ENV.BASE_URL || 'http://host.docker.internal:8787';

// What status counts as a successful resolve depends on which layer we hit:
//   :8787  → edge Worker → 302 redirect to the long URL
//   :8080  → origin direct → 200 + JSON body
//   :8090  → nginx LB → origin → 200 + JSON body
//   anything else → treat as edge (302) by default.
// Detection by port is good enough for this harness.
export const EXPECT_STATUS = BASE_URL.includes(':8787') ? 302 : 200;

export function isOkResolve(res) {
  return res.status === EXPECT_STATUS;
}

export function mintCodes(http, n, label) {
  const codes = [];
  for (let i = 0; i < n; i++) {
    const res = http.post(
      `${BASE_URL}/shorten`,
      JSON.stringify({ url: `https://example.com/${label}/${i}/${Date.now()}` }),
      { headers: { 'Content-Type': 'application/json' }, redirects: 0 }
    );
    if (res.status === 201) codes.push(res.json('code'));
  }
  return codes;
}

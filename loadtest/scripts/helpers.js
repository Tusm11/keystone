// Shared helpers for all k6 scenarios.

export const BASE_URL = __ENV.BASE_URL || 'http://host.docker.internal:8787';

// Edge returns 302 redirects on resolve; origin returns 200 + JSON body.
// Both are "success" — same lookup logic, different response shape.
// We detect which target we're hitting by port.
export const EXPECT_STATUS = BASE_URL.includes(':8080') ? 200 : 302;

export function isOkResolve(res) {
  return res.status === EXPECT_STATUS;
}

// Sequentially mint N short links; returns the array of codes.
// Minting goes through whichever BASE_URL points at:
//   edge (:8787)  → shortening is proxied to origin
//   origin (:8080) → direct origin call
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

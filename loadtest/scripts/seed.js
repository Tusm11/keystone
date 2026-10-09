// DEPRECATED — no longer needed.
//
// Each scenario now mints its own codes inside setup() and passes them
// to its VUs via setup's return value. This matches k6's execution
// model, which runs setup() and VU default() in separate JS VMs — the
// setup return value is the only reliable path for arbitrary data to
// reach VUs. Module-level mutations don't cross the VM boundary.
//
// You can delete this file:  del scripts\seed.js
console.log('seed.js is deprecated — each scenario self-seeds via setup()');

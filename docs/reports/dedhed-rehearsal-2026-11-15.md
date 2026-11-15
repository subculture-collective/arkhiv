# Dedhed compatibility rehearsal follow-up, November 15, 2026

This is a copy-only rehearsal for [issue 104](https://github.com/subculture-collective/arkhiv/issues/104).
Production Dedhed continues to use its existing backend. No migration, schedule
renewal, acquisition or public cutover is part of this change.

The earlier comparison found missing `Cache-Control: no-store` on unmatched
routes and different error text for absent or non-public audio. The compatibility
handler now supplies Dedhed's default response headers before routing and retains
its missing-recording and unavailable-file wording. The native core contracts
are unchanged.

The copied station has 90 programs using two recordings. Each poll previously
resolved and authorized the same transport again for every repeat. Three fresh
baseline polls took 2.18–2.29 seconds, near the three-second request deadline;
the earlier 60-poll run included one 503. A synthetic database regression test
reproduced the repeated work: 181 queries for 90 programs versus five for two.

Each poll now resolves a distinct item/representation pair once. The cache is
local to that request: later polls recheck current and future permissions, each
program still checks its duration, and the current file still passes integrity
verification. The repeated-program test now uses five queries. Existing tests
for future withdrawal, playback withdrawal, file replacement and invalidation
pass with the longer schedule.

Against the unchanged current Dedhed backend (`9c9b1c2`), the candidate passed
116 route pairs and 236 requests apart from five expected raw differences:
one build revision and two multipart boundary/header pairs. Separate multipart
checks replaced only the random boundary and confirmed matching body hashes for
both recordings. Eight paired station samples matched recording, track and
clock-adjusted offset; candidate requests took 61–91 milliseconds during that
comparison.

This does not establish exact parity with the older production backend. The
previous live-versus-current-Dedhed comparison had 446 raw differences across
20 request cases, including filters that production ignores, expanded search,
invalid-date/year validation, build identity and multipart/range behavior.
These are distinct from the core compatibility defects fixed here. Do not
silently normalize semantic differences or close issue 104 as field-for-field
production parity. Review the intended current filter behavior before cutover.

Validation: isolated `t3-dev --profile vhness verify`, focused app/audio tests
with the race detector, and golangci-lint 2.15.0 against the changed Go code.
The original browser rehearsal passed full-recording, reviewed-set and station
playback against the earlier candidate; it was not repeated for this patch.
Private raw comparison and timing evidence remains on Almaz under the existing
`rehearsal-dedhed-20261114/compare` directory.

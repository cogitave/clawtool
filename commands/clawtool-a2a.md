---
description: Inspect this clawtool instance's A2A Agent Card — the JSON contract peers will see when phase 2 lands the HTTP/mDNS surface.
allowed-tools: mcp__clawtool__Bash
---

Show the user this clawtool instance's A2A Agent Card. Phase 1 is
card-only — no HTTP server, no mDNS announce yet — but the card
itself is already a stable contract.

```bash
clawtool a2a card
```

Optional name override (useful when one operator runs multiple
clawtool instances on the same host):

```bash
clawtool a2a card --name my-laptop
```

Then explain to the user (in plain language):

- **What an Agent Card is**: A2A's discovery primitive. JSON
  document at `/.well-known/agent-card.json` (when the server lands).
  Describes capabilities + skills + auth schemes + protocol version
  the agent speaks. Peers fetch it once and decide whether to talk.
- **What the card claims**: 5 canonical skills (research / code-read
  / code-edit / agent-dispatch / shell), text+JSON I/O modes,
  protocol v0.2.x.
- **What's NOT exposed**: every internal tool. Per A2A's opacity
  model, peers see the contract, not the private surface.
- **Phase status**: card-only today. Phase 2 wires the HTTP
  endpoint; phase 3 ships mDNS LAN discovery; phase 4 layers
  per-peer capability tiers (Tier 0 metadata default-allow,
  Tier 1+ requires explicit grant).

Hard rule: **never mark a capability `true` unless the
implementation actually serves it.** Peers will trust the card
and try to use what we advertise.

---
title: "OAM reference: contributor notes"
weight: 30
---

Each page here belongs to one part of the OAM and component reference
(`api-reference/oam` and `api-reference/oam-components`). It holds what a
contributor needs and a reader of the reference does not: the code that
implements an entry, the issues behind it, and the tests that hold its claims.

A page is named after its reference part and has one section per entry, with up
to three lists:

- **Implementation**: the helper or file that builds the entry.
- **History**: the issues that introduced or changed it.
- **Tests**: the tests that hold a claim of the reference, with the claim each
  one holds.

Text on these pages is moved from the reference without rewording. A claim stays
in the reference; its test or issue reference moves here.

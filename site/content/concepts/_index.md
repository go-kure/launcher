---
title: "Concepts"
weight: 20
---

The launcher design, the OAM data model, and the decisions behind them. Read in this order:

1. [Model and Layers](oam-model/): the documents, components, traits, policies and
   lowering, traced from one authored document to the objects it builds.
2. [Design](design/): why launcher is a library with a command-line tool on it, and how it
   relates to kure.
3. [Delivery Scope](delivery-scope/): what the library, `kurel` and a downstream consumer
   each own, and what is not implemented. It is a dated design record, so check its status
   line before relying on a section.
4. [API Group & GVK](oam-gvk/), [Cluster Profiles](oam-cluster-profile/) and
   [Parameter Syntax](oam-param-syntax/): the formats you author.

The remaining pages in the sidebar are design records for single decisions.

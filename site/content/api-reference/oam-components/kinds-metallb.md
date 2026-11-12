---
title: "Kinds: MetalLB"
weight: 9
---

Each kind below has the same sections: Properties, Outputs, Validation and
Example. Its implementation, history and tests are on the
[contributor page](../../contributing/oam-reference/kinds-metallb.md).

## metallb-bfdprofile

`metallb-bfdprofile` is the kind-named projection of a fourth object of
MetalLB's `metallb.io/v1beta1` API: a BFDProfile.

### Properties

**What it changes for others.** A profile is the settings of a BFD
session: the intervals at which control packets are sent and expected
(`receiveInterval`, `transmitInterval`, `echoInterval`, in milliseconds),
the multiplier that turns an interval into the time after which the
connection to a peer is taken for lost (`detectMultiplier`), the echo and
passive modes, and the minimum time-to-live of a multi-hop session's
packets (`minimumTtl`). On its own it changes nothing: a MetalLB BGPPeer
names the profile its session uses (`bfdProfile`), and a peer that names
none has no BFD session. The profile then decides how fast the loss of
that peer is noticed.

**Authored.** The properties are the seven top-level json fields of
`BFDProfileSpec`, decoded strictly: an unknown key is refused. None is
required, and a component that authors none builds, as `spec: {}`.
- **No default is filled, and every authored value is written.** Each
  field is a pointer: an authored `echoMode: false` or `passiveMode: false`
  is written, and so is an authored 0. The CRD declares no default. The
  values MetalLB uses for an unset field are MetalLB's: the comments of its
  type name 300 ms for the receive and the transmit interval and 50 ms for
  the echo interval, which launcher neither fills nor checks.

**Required**: nothing.

### Outputs

It emits that one object, named after the component unless `objectName`
names it; the handler adds no label, no annotation and no default of its
own.

**The object is namespaced, and MetalLB reads it in one namespace only**,
as it reads a pool: it is written in the build namespace, and a profile of
an application built for another namespace than the one MetalLB watches is
an object MetalLB does not read. Launcher does not know that namespace and
checks nothing of it.

**Labels and annotations** are the `labels` and `annotations` properties.
A BGPPeer refers to the profile by its name: name the component so, or set
`objectName`.

### Validation

**No capability is required, and none gates the kind**: where MetalLB's
CRDs are not installed the component builds, and the object is refused at
apply. Whoever may author a component of an application built for MetalLB's
namespace may author a profile, and may so change the timers of the peers
that already name one of that name. A policy can keep the kind out of a
build through the object kind policy (`oam.ObjectKindPolicy`).

**The CRD's expression rules.** The CRD declares none.

**Not checked**, and MetalLB's or the API server's to refuse:
- **MetalLB's validating webhook was not read, and nothing it refuses is
  repeated here.** An object the CRD's schema takes may still be refused at
  apply by the webhook MetalLB installs;
- the bounds the CRD sets on the numbers (10 to 60000 for an interval, 2 to
  255 for the multiplier, 1 to 254 for the time-to-live): a value outside
  them is written as authored;
- whether the modes fit the session: the type's comment says the echo mode
  is not supported on multi-hop setups, and `minimumTtl` is for multi-hop
  sessions only.

**Policy.** No field of the spec is one an `oam.Policy` method speaks to, so
`ApplyPolicy` enforces nothing and fills nothing, and the kind builds the
same under every policy and under none. No field names a host or an image,
and none holds a literal secret or refers to a Secret.

**Not covered.** Whether a peer names the profile. The object's status is
MetalLB's and is not written: the type has no field in it, and the YAML
the library writes leaves an empty status out.

### Example

The component in `pkg/cmd/kurel/testdata/metallb-bfdprofile-component/app.yaml`,
built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it.
The fixture test compares the two.

## metallb-bgpadvertisement

`metallb-bgpadvertisement` is the kind-named projection of a third object of
MetalLB's `metallb.io/v1beta1` API: a BGPAdvertisement.

### Properties

**What it changes for others.** A BGPAdvertisement makes MetalLB announce
to the cluster's BGP peers the addresses it gave to Services from address
pools, and sets what the routes carry. No field is required:
- `ipAddressPools` names the pools and `ipAddressPoolSelectors` selects
  them by their labels. With no pool named or selected the advertisement
  applies to every pool MetalLB reads;
- `peers` limits the BGP peers the addresses are announced to. With none,
  they are announced to every BGP peer MetalLB is configured with;
- `nodeSelectors` limits the nodes that are announced as next hops for an
  address. With none, the advertisement excludes no node;
- `serviceSelectors` limits the Services. With none, every Service that
  has an address of the selected pools is announced;
- `aggregationLength` and `aggregationLengthV6` roll the addresses up into
  a larger prefix, of that length, which is then what is announced in
  place of one route per address;
- `localPref`, `asPathPrepend` and `communities` are attributes of the
  announcement: BGP's best path selection prefers a path with a higher
  LOCAL_PREF, `asPathPrepend` lengthens the AS path on purpose by that
  many copies of the local ASN, and the communities are attached to what
  the peers receive.

**A component that authors no property is the widest advertisement there
is**: every pool, to every peer, for every Service, and no node excluded.
It builds, as `spec: {}`.

**Authored.** The properties are the top-level json fields of
`BGPAdvertisementSpec`, decoded strictly at every depth: an unknown key is
refused wherever it sits (the spec, a selector, a match expression).
- **No default is filled, and an authored value is written where the Go
  type can hold it.** The two aggregation lengths are pointers: an authored
  one is written, also where it is the value the API would fill, and an
  unauthored one is left out, which the API fills with 32 and 128, one
  route per address.
- `localPref` is a number the type omits at 0, with no default in the CRD:
  an authored `localPref: 0` is left out, and is the same advertisement to
  MetalLB as one that does not author the field, since its type holds one
  value for both. That is not an advertisement without LOCAL_PREF: what
  MetalLB sends is its BGP backend's choice (at MetalLB v0.16.1 the native
  backend sends LOCAL_PREF 0 on an iBGP session; the FRR backends set no
  local preference). An authored empty list is left out too, as the type
  omits it: it is the same advertisement as one that does not author the
  field.
- `asPathPrepend` is a number from 1 to 10 that the type omits at 0, so an
  authored `asPathPrepend: 0` is left out and is the same advertisement as
  one that does not author the field. MetalLB supports it only in its
  frr-k8s mode and refuses the configuration in another; launcher cannot
  see the mode and does not enforce that, nor the range, which the API
  server checks. Advertisements that overlap must carry the same value,
  which MetalLB checks and launcher does not.

**Required**: the `key` and the `operator` of a match expression, in every
selector of `ipAddressPoolSelectors`, `nodeSelectors` and
`serviceSelectors`. The API requires nothing else of the spec. The list is
generated from the linked module's `v1beta1` CRD (see
["Required fields a kind writes unauthored and does not refuse"](conventions.md#required-fields-a-kind-writes-unauthored-and-does-not-refuse)).

### Outputs

It emits that one object, named after the component unless `objectName`
names it; the handler adds no label, no annotation and no default of its
own.

**The object is namespaced, and MetalLB reads it in one namespace only**,
as it reads a pool: it is written in the build namespace, and an
advertisement of an application built for another namespace than the one
MetalLB watches is an object MetalLB does not read. Launcher does not know
that namespace and checks nothing of it.

**Labels and annotations** are the `labels` and `annotations` properties.
The pools `ipAddressPools` names are the object names of
`metallb-ipaddresspool` components (the component's name, or its
`objectName`), and the labels `ipAddressPoolSelectors` queries are their
`labels`. The other two selectors query objects launcher does not write
the labels of here (nodes, Services) and are the author's.

### Validation

**No capability is required, and none gates the kind**: where MetalLB's
CRDs are not installed the component builds, and the object is refused at
apply. Whoever may author a component of an application built for MetalLB's
namespace may author an advertisement, and with it which addresses the
cluster announces to its BGP peers and with which attributes. A policy can
keep the kind out of a build through the object kind policy
(`oam.ObjectKindPolicy`).

**The CRD's expression rule is checked.** The CRD declares one, on the
spec: a service selector is taken only with one route per address. A
component that authors a non-empty `serviceSelectors` beside an
`aggregationLength` other than 32 or an `aggregationLengthV6` other than
128 is refused (`serviceSelectors: not allowed with aggregationLength 24:
…`). The API server evaluates the rule after it has filled its defaults,
which are those two values, so an unauthored length keeps the rule, as one
authored at that value does, and so does an empty list of selectors.

**Not checked**, and MetalLB's or the API server's to refuse:
- **MetalLB's validating webhook was not read, and nothing it refuses is
  repeated here.** An object the CRD's schema takes may still be refused at
  apply by the webhook MetalLB installs;
- the CRD's other value rules: an `aggregationLength` below 1 is written
  as authored;
- a community: no entry of `communities` is read, neither its form nor
  whether a Community object defines the alias it names;
- a pool's name and a peer's name: one that names no object of the
  application builds;
- an authored empty value in a required field (a `key: ""`). It is a value.

**Policy.** No field of the spec is one an `oam.Policy` method speaks to, so
`ApplyPolicy` enforces nothing and fills nothing, and the kind builds the
same under every policy and under none. No field names a host or an image,
and none holds a literal secret or refers to a Secret.

**Not covered.** Whether what is named or selected exists (a pool, a peer,
a community alias, a node, a Service). The object's status is MetalLB's
and is not written: the type has no field in it, and the YAML the library
writes leaves an empty status out.

### Example

The component in `pkg/cmd/kurel/testdata/metallb-bgpadvertisement-component/app.yaml`,
built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it.
The fixture test compares the two.

## metallb-bgppeer

`metallb-bgppeer` is the kind-named projection of a sixth object of
MetalLB's API: a BGPPeer.

### Properties

**What it changes for others.** A BGPPeer is a router MetalLB holds a BGP
session with, from the nodes that match one of the `nodeSelectors`, and
from every node where the list is unset or empty; selectors that match no
node leave the peer with no session. Over that session the cluster announces the
addresses its BGP advertisements cover: an advertisement that names no peer
announces to every peer, so a new peer receives those announcements without
any advertisement being changed. The fields say who the peer is and how the
session runs:
- `myASN` is the AS number of the cluster's end, `peerASN` the one expected
  of the router, or `dynamicASN` (`internal` or `external`) where it is
  detected; `localASN` announces another AS number to this peer;
- `peerAddress` is the address dialled, or `interface` the node's interface
  of an unnumbered session; `peerPort`, `sourceAddress`, `routerID` and
  `vrf` are the rest of the connection;
- `holdTime`, `keepaliveTime` and `connectTime` are the session's timers;
- `password` or `passwordSecret` authenticates the session (TCP MD5);
- `bfdProfile` names the BFDProfile of the session's BFD session, and a
  peer that names none has no BFD session;
- `enableGracefulRestart`, `ebgpMultiHop`, `disableMP` (deprecated upstream
  in favour of `dualStackAddressFamily`) and `dualStackAddressFamily` are
  switches of the session.

**Authored.** The properties are the twenty-one top-level json fields of
`BGPPeerSpec`, decoded strictly at every depth: an unknown key is refused
wherever it sits (the spec, the Secret reference, a selector, a match
expression).
- **No default is filled.** The CRD declares three. `disableMP` and
  `dualStackAddressFamily` default to `false`, which is the value the type
  leaves out: an authored `false` is left out, and the API fills the same
  `false` back. `peerPort` defaults to 179 and the type leaves a 0 out, so
  an authored `peerPort: 0` would become 179: it is refused (`peerPort: 0
  cannot be carried by the MetalLB API types (…)`). An unauthored port is
  left out, and the API fills 179.
- **An authored 0, false or empty string on any other field the type omits
  when empty is left out**, as the type holds one value for it and for a
  peer that does not author the field: `peerASN`, `localASN`, the two other
  switches, the addresses and names. For `localASN` that is a value under
  the CRD's minimum of 1, which the API server therefore does not see.
- The three timers are pointers: an authored `holdTime: 0s` is written. A
  duration is written in the form the type gives it (`90s` as `1m30s`).
- `myASN` is always encoded: an authored 0 is written.
- **`passwordSecret: {}` is written where no reference is authored.** The
  type holds the reference by value and always encodes it. The CRD takes
  it, and MetalLB v0.16.1 reads a reference with no name as none.

**Required**: `myASN`, and the `key` and the `operator` of a match
expression in every selector of `nodeSelectors`. The API requires nothing
else of the spec. The list is generated from the linked module's `v1beta2`
CRD (see ["Required fields a kind writes unauthored and
does not refuse"](conventions.md#required-fields-a-kind-writes-unauthored-and-does-not-refuse)).

### Outputs

It is built at `metallb.io/v1beta2`, not at the `v1beta1` of the five kinds
above: that is the version the linked module's CRD stores, `v1beta1` is
served and marked deprecated there, and the base library's constructor
builds the peer at `v1beta2`. It emits that one object, named after the
component unless `objectName` names it; the handler adds no label, no
annotation and no default of its own.

**The object is namespaced, and MetalLB reads it in one namespace only**,
as it reads a pool: it is written in the build namespace, and a peer of an
application built for another namespace than the one MetalLB watches is an
object MetalLB does not read. Launcher does not know that namespace and
checks nothing of it.

**Labels and annotations** are the `labels` and `annotations` properties.
A BGPAdvertisement names the peers it announces to (`peers`): name the
component so, or set `objectName`. The profile `bfdProfile` names is the
object name of a `metallb-bfdprofile` component (the component's name, or
its `objectName`). `nodeSelectors` queries nodes, whose labels launcher
does not write, and is the author's.

### Validation

**No capability is required, and none gates the kind**: where MetalLB's
CRDs are not installed the component builds, and the object is refused at
apply. Whoever may author a component of an application built for MetalLB's
namespace may author a peer, and with it a router that the cluster's nodes
connect to and that is told the addresses of the cluster's Services. A
policy can keep the kind out of a build through the object kind policy
(`oam.ObjectKindPolicy`).

**Two of the CRD's three expression rules are checked.** Both are on
`connectTime`: it is from 1 to 65535 seconds, and it is a whole number of
seconds. A `connectTime` that breaks one is refused (`connectTime: 1.5s is
not a whole number of seconds, which the API requires`). The kind reads a
duration as the API server's rules do, in whole seconds and whole
milliseconds with the rest dropped: `65535.5s` passes the first rule and
breaks the second, and a part smaller than a millisecond is seen by
neither, here or there. **The third rule is left**: `enableGracefulRestart` may not
change after creation. It compares the field with the stored object's,
which a build does not have, and the API server does not evaluate it on a
create.

**Not checked**, and MetalLB's or the API server's to refuse:
- **MetalLB's validating webhook was not read, and nothing it refuses is
  repeated here.** An object the CRD's schema takes may still be refused at
  apply by the webhook MetalLB installs;
- what MetalLB itself requires of a peer when it reads its configuration.
  At v0.16.1 that is, among others: a `myASN` other than 0; exactly one of
  `peerASN` and `dynamicASN`; exactly one of `peerAddress` and `interface`;
  not both `password` and a named `passwordSecret`. A component that breaks
  one of these builds;
- the CRD's other value rules: a `peerPort` above 16384 and a `dynamicASN`
  that is neither `internal` nor `external` are written as authored;
- an address, a router ID and a VRF's or an interface's name: none is read;
- the profile `bfdProfile` names and the Secret `passwordSecret` names:
  one that names no object of the application builds;
- an authored empty value in a required field (a `key: ""`). It is a value.

**Policy.** The environment policy reaches one field.
- **`password` is the session's password in the clear, and is refused under
  a policy that forbids explicit secrets** (`password: holds the BGP session
  password in the object, and the environment policy forbids explicit
  secrets; name a Secret created out of band in passwordSecret instead`), as
  the keystore password of a `certificate` is. The refusal does not carry
  the value. Under a policy that allows explicit secrets, under one that
  does not answer the question and under none the peer builds, with the
  password in the object as authored. An empty `password` is none.
- **`passwordSecret` names a Secret and holds no secret**, and builds under
  every policy. Launcher does not create that Secret and does not check
  that it exists. The upstream type's comment asks for one of type
  `kubernetes.io/basic-auth`, in the namespace of the MetalLB deployment, with the
  password under the key `password`; none of that is checked, and neither
  is the reference's `namespace`.
- **Hosts are not checked.** The addresses a peer holds are a network's,
  not an artifact source, and none is held to the policy's allowed
  registries. No field names an image.
- No other `oam.Policy` method speaks to a field of the spec, and
  `ApplyPolicy` fills nothing.

**Not covered.** Whether what is named or selected exists (a profile, a
Secret, a node), and whether the router is reachable. The object's status
is MetalLB's and is not written: the type has no field in it, and the YAML
the library writes leaves an empty status out.

### Example

The component in `pkg/cmd/kurel/testdata/metallb-bgppeer-component/app.yaml`,
built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it.
The fixture test compares the two.

## metallb-community

`metallb-community` is the kind-named projection of a fifth object of
MetalLB's `metallb.io/v1beta1` API: a Community.

### Properties

**What it changes for others.** A Community defines aliases: each gives a
`name` to one BGP community `value`, a standard one of the form
`1234:1234` or a large one of the form `large:1234:1234:1234`. On its own
it announces nothing. An item of a MetalLB BGPAdvertisement's
`communities` may be the name of such an alias, and the advertisement then
attaches the alias's value to what the cluster announces to its BGP peers.
Routers that receive a route may act on its communities, so the value
behind a name bears on how the networks beyond the cluster treat the
addresses announced under it.

**Authored.** The one property is the top-level json field of
`CommunitySpec`, `communities`, decoded strictly: an unknown key is
refused, in the spec and in an alias. It is not required, and a component
that authors none builds, as `spec: {}`.
- **No default is filled.** The CRD declares none. The `name` and the
  `value` of an alias are strings the upstream type leaves out when empty,
  and the CRD requires neither: an alias authored with an empty one is
  written without it, and an alias that authors nothing is written as an
  empty item.

**Required**: nothing.

### Outputs

It emits that one object, named after the component unless `objectName`
names it; the handler adds no label, no annotation and no default of its
own.

**The object is namespaced, and MetalLB reads it in one namespace only**,
as it reads a pool: it is written in the build namespace, and a Community
of an application built for another namespace than the one MetalLB watches
is an object MetalLB does not read. Launcher does not know that namespace
and checks nothing of it.

**Labels and annotations** are the `labels` and `annotations` properties.
An advertisement refers to an alias by the alias's `name`, not by the name
of the object that holds it.

### Validation

**No capability is required, and none gates the kind**: where MetalLB's
CRDs are not installed the component builds, and the object is refused at
apply. Whoever may author a component of an application built for MetalLB's
namespace may author a Community, and may so define a name an
advertisement there already uses. What MetalLB does with a name that two
aliases define, in one object or in two, was not read. A policy can keep
the kind out of a build through the object kind policy
(`oam.ObjectKindPolicy`).

**The CRD's expression rules.** The CRD declares none.

**Not checked**, and MetalLB's or the API server's to refuse:
- **MetalLB's validating webhook was not read, and nothing it refuses is
  repeated here.** An object the CRD's schema takes may still be refused at
  apply by the webhook MetalLB installs;
- the form of a value: one that is neither a standard nor a large
  community is written as authored;
- an alias without a name or without a value, and a name defined twice.

**Policy.** No field of the spec is one an `oam.Policy` method speaks to, so
`ApplyPolicy` enforces nothing and fills nothing, and the kind builds the
same under every policy and under none. No field names a host or an image,
and none holds a literal secret or refers to a Secret.

**Not covered.** Whether an advertisement names an alias, and whether a name
an advertisement uses is defined. The object's status is MetalLB's and is
not written: the type has no field in it, and the YAML the library writes
leaves an empty status out.

### Example

The component in `pkg/cmd/kurel/testdata/metallb-community-component/app.yaml`,
built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it.
The fixture test compares the two.

## metallb-ipaddresspool

`metallb-ipaddresspool` is the kind-named projection of an object of
MetalLB's `metallb.io/v1beta1` API: an IPAddressPool.

### Properties

**What it changes for others.** A pool is the address ranges MetalLB has
authority over and gives to Services of type LoadBalancer. With no
`serviceAllocation` the pool is limited to no namespace and no Service: a
Service of any namespace of the cluster may be given one of its addresses.
`serviceAllocation` limits it to the namespaces it lists or selects and to
the Services it selects. `autoAssign: false` keeps MetalLB from allocating
from the pool on its own.

**Authored.** The properties are the top-level json fields of
`IPAddressPoolSpec`, decoded strictly at every depth: an unknown key is
refused wherever it sits (the spec, the allocation, a selector).
- `addresses`, each a CIDR prefix or a first and a last address joined by a
  dash; `autoAssign`; `avoidBuggyIPs`; `serviceAllocation`, with its
  `priority`, `namespaces`, `namespaceSelectors` and `serviceSelectors`.
- **No default is filled, and an authored value that is the API's default is
  not written where the Go type cannot hold it.** `autoAssign` is a pointer:
  an authored `false` is written, and an unauthored one is left out, which
  the API fills with `true`. `avoidBuggyIPs` is no pointer and is omitted
  when false: an authored `false` is left out of the object, and the API
  fills the same `false` back. An allocation's
  `priority` is a number the type omits at 0, with no default in the CRD:
  an authored `priority: 0` is an allocation with no priority, as MetalLB's
  own type reads it.

**Required**: a field the API requires that the Go type writes whether or
not it was authored must be authored (`addresses: required (…)`):
- `addresses`;
- the `key` and the `operator` of a match expression, in every selector of
  `serviceAllocation.namespaceSelectors` and
  `serviceAllocation.serviceSelectors`.

An authored empty value satisfies the rule: `addresses: []` is a pool with
no address, which the CRD's schema takes. The list is generated from the
fields the linked module's `v1beta1` CRD requires and the type writes
unauthored (see ["Required fields a kind writes
unauthored and does not refuse"](conventions.md#required-fields-a-kind-writes-unauthored-and-does-not-refuse)).

### Outputs

It emits that one object, named after the component unless `objectName`
names it; the handler adds no label, no annotation and no default of its
own.

**The object is namespaced, and MetalLB reads it in one namespace only.** It
is written in the build namespace. MetalLB reads the objects of its API in
one namespace and in no other: the one it is configured to watch, by its
`--namespace` flag or the `METALLB_NAMESPACE` variable, and by default the
one it runs in. A pool of an application built for another namespace is an
object MetalLB does not read. Launcher does not know that namespace and
checks nothing of it.

**Labels and annotations** are the `labels` and `annotations` properties.
**A pool is referred to by its name, or selected by its labels.** A MetalLB
advertisement names the pools it announces (`ipAddressPools`): name the
component so, or set `objectName`. Its `ipAddressPoolSelectors` is a label
query over pools: write the labels it names under the `labels` of the
`metallb-ipaddresspool`. The object also carries the component label, whose
value is the component's (see "Component label and ownership" in the OAM
model). The selectors of `serviceAllocation` query objects launcher does
not write the labels of here (namespaces, Services) and are the author's.

### Validation

**No capability is required, and none gates the kind**: where MetalLB's
CRDs are not installed the component builds, and the object is refused at
apply. Whoever may author a component of an application built for MetalLB's
namespace may author a pool, and with it which Services of the cluster get
an address of which range. A policy can keep the kind out of a build
through the object kind policy (`oam.ObjectKindPolicy`).

**The CRD's expression rules.** The CRD declares none.

**Not checked**, and MetalLB's or the API server's to refuse:
- **MetalLB's validating webhook was not read, and nothing it refuses is
  repeated here.** An object the CRD's schema takes may still be refused at
  apply by the webhook MetalLB installs;
- an address: no entry of `addresses` is read, and a string that is no
  range builds;
- an authored empty value in a required field (a `key: ""`). It is a value.

**Policy.** No field of the spec is one an `oam.Policy` method speaks to, so
`ApplyPolicy` enforces nothing and fills nothing, and the kind builds the
same under every policy and under none.
- **Hosts are not checked.** No field names a host. An address or a range a
  pool holds is a network's, not an artifact source, and none is held to
  the policy's allowed registries.
- **No field holds a literal secret or refers to a Secret, and none is
  checked.**

**Not covered.** Whether what is selected exists (a namespace, a Service).
The pool's status is MetalLB's and is not written: the Go type always
encodes its four counters, at zero, and the YAML the library writes leaves
a status that holds nothing else out.

### Example

The component in `pkg/cmd/kurel/testdata/metallb-ipaddresspool-component/app.yaml`,
built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it.
The fixture test compares the two.

## metallb-l2advertisement

`metallb-l2advertisement` is the kind-named projection of another object of
MetalLB's `metallb.io/v1beta1` API: an L2Advertisement.

### Properties

**What it changes for others.** An L2Advertisement makes MetalLB announce
on the local network (layer 2) the addresses it gave to Services from
address pools. Every field narrows it, and none is required:
- `ipAddressPools` names the pools and `ipAddressPoolSelectors` selects
  them by their labels. With no pool named or selected the advertisement
  applies to every pool MetalLB reads;
- `nodeSelectors` limits the nodes that are announced as next hops for an
  address. With none, the advertisement excludes no node;
- `preferredNodeSelectors` ranks the nodes `nodeSelectors` admits: each
  entry has a `weight` from 1 to 100 and a `preference`, a label selector,
  and a node that matches it scores that weight (the weights of the
  entries it matches add up). It never excludes a node;
- `interfaces` lists the interfaces of a node the address is announced
  from. With none, it is announced from all of them;
- `serviceSelectors` limits the Services. With none, every Service that
  has an address of the selected pools is announced.

**A component that authors no property is the widest advertisement there
is**: every pool, every interface, every Service, and no node excluded. It
builds, as `spec: {}`.

**Authored.** The properties are the top-level json fields of
`L2AdvertisementSpec`, decoded strictly at every depth: an unknown key is
refused wherever it sits (the spec, a selector, a match expression). The
CRD declares no default on any of them, and none is filled. An authored
empty list is left out, as the type omits it: it is the same advertisement
as one that does not author the field.

**Required**: the `key` and the `operator` of a match expression, in every
selector of `ipAddressPoolSelectors`, `nodeSelectors`, `serviceSelectors`
and the `preference` of each `preferredNodeSelectors` entry (`nodeSelectors[1].matchExpressions[0].key:
required (…)`), and the `weight` and `preference` of each entry
(`preferredNodeSelectors[0].weight: required (…)`). The API requires
nothing else of the spec. The list is generated
from the linked module's `v1beta1` CRD (see ["Required
fields a kind writes unauthored and does not refuse"](conventions.md#required-fields-a-kind-writes-unauthored-and-does-not-refuse)).

### Outputs

It emits that one object, named after the component unless `objectName`
names it; the handler adds no label, no annotation and no default of its
own.

**The object is namespaced, and MetalLB reads it in one namespace only**,
as it reads a pool: it is written in the build namespace, and an
advertisement of an application built for another namespace than the one
MetalLB watches is an object MetalLB does not read. Launcher does not know
that namespace and checks nothing of it.

**Labels and annotations** are the `labels` and `annotations` properties.
The pools `ipAddressPools` names are the object names of
`metallb-ipaddresspool` components (the component's name, or its
`objectName`), and the labels `ipAddressPoolSelectors` queries are their
`labels`. The other two selectors query objects launcher does not write
the labels of here (nodes, Services) and are the author's.

### Validation

**No capability is required, and none gates the kind**: where MetalLB's
CRDs are not installed the component builds, and the object is refused at
apply. Whoever may author a component of an application built for MetalLB's
namespace may author an advertisement, and with it which addresses the
cluster announces on its local network, from which nodes and interfaces.
A policy can keep the kind out of a build through the object kind policy
(`oam.ObjectKindPolicy`).

**The CRD's expression rules.** The CRD declares none.

**Not checked**, and MetalLB's or the API server's to refuse:
- **MetalLB's validating webhook was not read, and nothing it refuses is
  repeated here.** An object the CRD's schema takes may still be refused at
  apply by the webhook MetalLB installs;
- a pool's name: no entry of `ipAddressPools` is read, and one that names
  no pool of the application builds;
- an interface's name;
- an authored empty value in a required field (a `key: ""`). It is a value.

**Policy.** No field of the spec is one an `oam.Policy` method speaks to, so
`ApplyPolicy` enforces nothing and fills nothing, and the kind builds the
same under every policy and under none. No field names a host or an image,
and none holds a literal secret or refers to a Secret.

**Not covered.** Whether what is named or selected exists (a pool, a node,
an interface, a Service). The object's status is MetalLB's and is not
written: the type has no field in it, and the YAML the library writes
leaves an empty status out.

### Example

The component in `pkg/cmd/kurel/testdata/metallb-l2advertisement-component/app.yaml`,
built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it.
The fixture test compares the two.

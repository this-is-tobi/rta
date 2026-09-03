# Changelog

## [0.4.0](https://github.com/this-is-tobi/rule-them-all/compare/v0.3.0...v0.4.0) (2026-09-03)


### Features

* **agentlog:** split the code from the sentence - stable event codes on every row ([da55cb2](https://github.com/this-is-tobi/rule-them-all/commit/da55cb2b86cf0d8e7fe7fa0806e47f90563cdcc9))
* **kube:** provision grants logs, workloads, services and rollout - and the input completes ([5f9a64f](https://github.com/this-is-tobi/rule-them-all/commit/5f9a64f600819a07646f362d1f1ff09e8d646ab2))
* **mysql,mariadb:** dump and restore a database, for a person at a terminal ([3eb384a](https://github.com/this-is-tobi/rule-them-all/commit/3eb384a9518557dc6e34d70f86416dcbef6a157a))
* **operator:** roster rows take expires= - a departure date the clock enforces ([e1040ae](https://github.com/this-is-tobi/rule-them-all/commit/e1040ae2087c6f991d0e1c4ca45bc3750c9fd32b))
* **qdrant:** dump and restore a collection as a snapshot file, for a person at a terminal ([67a68d4](https://github.com/this-is-tobi/rule-them-all/commit/67a68d45df31c8c6b5f15e2ad117a3c7612c1b8e))
* **s3:** upload a directory into a bucket - bucket.download's other half ([33671f8](https://github.com/this-is-tobi/rule-them-all/commit/33671f8994f5aa54425fc99723907247332ae169))
* **vault:** restore a snapshot into a Vault, for a person at a terminal ([b538929](https://github.com/this-is-tobi/rule-them-all/commit/b538929dec0178755381afb2c00b1551b130513e))


### Bug Fixes

* **agentlog,operator,grant:** close the branch review findings ([3f86d61](https://github.com/this-is-tobi/rule-them-all/commit/3f86d61801b257d0b5d05e074d582e0c2eab9c0f))
* **image:** rta-full carries a MariaDB client, so its dump and restore run ([e994d97](https://github.com/this-is-tobi/rule-them-all/commit/e994d97f496bce4fad4949d158a749e29747100b))
* **mcp,plugin:** ledger a handler's policy gate as refused, not failed ([87d148d](https://github.com/this-is-tobi/rule-them-all/commit/87d148d6304e9086895e6025fc30128277e40090))

## [0.3.0](https://github.com/this-is-tobi/rule-them-all/compare/v0.2.0...v0.3.0) (2026-09-02)


### Features

* **agent:** pending, show, allow and deny gain --server - consent answered where you are ([3e54ccf](https://github.com/this-is-tobi/rule-them-all/commit/3e54ccf25fe9851d769e89f0550393160980112f))
* **consent:** digest-bound answers - DecideBound pins what was read ([256e996](https://github.com/this-is-tobi/rule-them-all/commit/256e996d58a2d18e196d0a216099e897e616535b))
* **grant:** allow and revoke gain --server - grants managed where you stand ([32a01ad](https://github.com/this-is-tobi/rule-them-all/commit/32a01adf63dc0bb179bbc433bdbbbfa33a823aab))
* **grant:** grant guard on/off/status - the passphrase gate's operator surface ([62a2f7f](https://github.com/this-is-tobi/rule-them-all/commit/62a2f7f4c48d232d2ec97dd67120d69f0584b481))
* **grant:** guard signatures over grant authority, enforced on read and issue ([19e80c7](https://github.com/this-is-tobi/rule-them-all/commit/19e80c7cd6d03c0bb435dd2cc0aa116c6b1a25dc))
* **grant:** rta grant guard remote - enroll a roster as this machine's guard ([085adc2](https://github.com/this-is-tobi/rule-them-all/commit/085adc2247171b5ce394078bf3ef180cc071316c))
* **grant:** the guard gates allow, renew and agent allow --ttl ([d610461](https://github.com/this-is-tobi/rule-them-all/commit/d6104617d7c81696d18252cae8dc8e51f4ef2082))
* **guard:** operator passphrase gate - key wrapping and signatures ([96f8993](https://github.com/this-is-tobi/rule-them-all/commit/96f89935655dd99ffa47415ead004918699e7f4c))
* **guard:** remote mode - a guard whose keys live with operators elsewhere ([eed8208](https://github.com/this-is-tobi/rule-them-all/commit/eed820854fe4286879d5ac3c758aa3304f8bedec))
* **lock:** freeze one principal now - the instant path revocation was missing ([c9c9bd0](https://github.com/this-is-tobi/rule-them-all/commit/c9c9bd0a722cebebcc2c38736e20a59bba4400ec))
* **mcp:** --consent starts beside --http when --operators names who answers ([1626535](https://github.com/this-is-tobi/rule-them-all/commit/162653575b4f54d3ebee4bf39148155465da2d1f))
* **mcp:** mount the operator channel beside the MCP endpoint ([665378e](https://github.com/this-is-tobi/rule-them-all/commit/665378efcaafad1fb7e5308fdac04591ebc7bfe7))
* **mcp:** the operator channel's consent verbs - list the queue, answer a call ([d79d1a8](https://github.com/this-is-tobi/rule-them-all/commit/d79d1a8890a1a3f23f6259721fafcb74726b1611))
* **mcp:** the operator channel's mutation verbs - revoke, prepare, issue ([0965912](https://github.com/this-is-tobi/rule-them-all/commit/0965912dd26c82c70d25556c41c681c4ea435041))
* **operator,agentlog:** ledger the channel's mutations, attributed to the signing key ([fe170bc](https://github.com/this-is-tobi/rule-them-all/commit/fe170bce89cbd8a23fa6baf1c4864449fae89832))
* **operator:** role=read roster rows - enrollment that watches but cannot act ([16dfa11](https://github.com/this-is-tobi/rule-them-all/commit/16dfa1152203880ae0aca958b5274e895ebbb3a1))
* **operator:** the client side - one CLI, many servers, HITL on every call ([a309f1e](https://github.com/this-is-tobi/rule-them-all/commit/a309f1e52e2132dd178359f398bc431be47ccd20))
* **operator:** the identity layer of the remote operator channel ([bd3d558](https://github.com/this-is-tobi/rule-them-all/commit/bd3d55897fcf14e198a5cf590433745b90baa8f7))
* **tui:** bare actions - the consent pane keeps its one-key answers ([b20825c](https://github.com/this-is-tobi/rule-them-all/commit/b20825c975cde9e881dc539043fbda4ea30467f7))


### Bug Fixes

* **consent,tui:** close the stage-3 review's deferred lows - orphan sweep, bare soundness ([5b57d9c](https://github.com/this-is-tobi/rule-them-all/commit/5b57d9c2cc4701d750fe4c58cea1c676f778dded))
* **deps:** upgrade x/crypto to v0.56.0 for the ssh channel DoS advisories ([9d29e90](https://github.com/this-is-tobi/rule-them-all/commit/9d29e908a43f7394e8b7d203b17b3ca1361e8140))
* **guard,mcp:** close the roster review's lows - drift warning, honest statuses ([3aa761c](https://github.com/this-is-tobi/rule-them-all/commit/3aa761c9696c8626771fcd5ffd7d9b0da259e56c))
* **guard:** close the review findings - argv channel, silent rollback, races ([14449e0](https://github.com/this-is-tobi/rule-them-all/commit/14449e082582aa466d7d8613ceaaa812982846f7))
* **guard:** refuse a remote guard before the prompt, not after the typing ([d997519](https://github.com/this-is-tobi/rule-them-all/commit/d997519d62734e2037840b6ab11cafe5dd7852fb))
* **lockdown,mcp:** close the lock review findings - credential grammar, parked race, recovery hint ([436f1fd](https://github.com/this-is-tobi/rule-them-all/commit/436f1fdec3c3331dcc15ccaa2277d467768c53a0))
* **mcp:** classify the wired packages' refusals - the ledger and the pager were both wrong ([c246b3a](https://github.com/this-is-tobi/rule-them-all/commit/c246b3a55c5ece8e0309c7fe598a75d044ca1545))
* **operator:** close the security review's findings - above all, bind the server ([20ce35c](https://github.com/this-is-tobi/rule-them-all/commit/20ce35c1e29e44feaf6d0d819ffbb238965008f6))
* **operator:** close the stage-2 review - bind grants to their server, verify before signing ([b278c3b](https://github.com/this-is-tobi/rule-them-all/commit/b278c3b3ce21ae2590d44f4bb012d5b9ceb22a45))
* **operator:** consent verbs answer only for the server that parks ([8b48dcd](https://github.com/this-is-tobi/rule-them-all/commit/8b48dcd47c0244a640100118b0d3eac7f5259359))
* **operator:** one spelling per key - strict base64, dedup by decoded bytes ([b23259d](https://github.com/this-is-tobi/rule-them-all/commit/b23259d3cd4c5efd2ac8e580614307240fc93671))


### Code Refactoring

* **guard:** extract the passphrase-wrapped-key shape into internal/passkey ([9cc9e65](https://github.com/this-is-tobi/rule-them-all/commit/9cc9e65470f972e63ebfe19f6e04ffbe0a43958f))

## [0.2.0](https://github.com/this-is-tobi/rule-them-all/compare/v0.1.0...v0.2.0) (2026-09-02)


### Features

* **agent:** repair a record that lost only its integrity mark ([76d2ef0](https://github.com/this-is-tobi/rule-them-all/commit/76d2ef081a5bb6b1bba3620ad26e85f3e6547ecc))
* **app:** state, remove and complete profile instances from the CLI ([5de2380](https://github.com/this-is-tobi/rule-them-all/commit/5de2380fb75ccf5495a3d0542ee03adf23cf304e))
* **audit:** audit.agents --fix prints the exact edit for each finding ([9cdc272](https://github.com/this-is-tobi/rule-them-all/commit/9cdc272e0f8ce017a4c3e459038a78ccae05e010))
* **config:** instance labels in profile plugin keys ([9274d96](https://github.com/this-is-tobi/rule-them-all/commit/9274d963f4f20a93373eeed80ce63c689321b421))
* **config:** print the file's JSON Schema for editor completion ([859590c](https://github.com/this-is-tobi/rule-them-all/commit/859590cc378b0203151e153a1d89e063e98a1ff9))
* **docker:** a batteries-included image beside the narrow one ([9ac06ba](https://github.com/this-is-tobi/rule-them-all/commit/9ac06baa44eebf355bb5517d2007fb50e0e34ae3))
* **grant:** per-instance consent ([732e308](https://github.com/this-is-tobi/rule-them-all/commit/732e30857b7ba05e3f5589b9118a6320223f2ecf))
* **kv:** say which profiles use each entry, off the agent surface ([b3ac28b](https://github.com/this-is-tobi/rule-them-all/commit/b3ac28b4cdc3d0459060696168b375589eed1a53))
* **profile:** read a credential from a cluster without forcing a forward ([a2130dc](https://github.com/this-is-tobi/rule-them-all/commit/a2130dc8722799ae8049b1d4ea20ffdb4d4501a6))
* **profile:** resolve instance refs, stamp per instance ([62aec35](https://github.com/this-is-tobi/rule-them-all/commit/62aec357b4ffa4f196764761f19499ca8a559daa))
* **tui:** arm profile deletes behind a second keypress ([645b517](https://github.com/this-is-tobi/rule-them-all/commit/645b517d471e6c60e7ad8fa89a70d3a255e51208))
* **tui:** edit, pick and read profile instances ([ed2babb](https://github.com/this-is-tobi/rule-them-all/commit/ed2babb8c5710d18ee2307f02a6878fde965d9fb))


### Bug Fixes

* **agent:** measure a parked answer's origin instead of assuming a person ([0a9d29b](https://github.com/this-is-tobi/rule-them-all/commit/0a9d29b799f0ce4bfac8ecee9e18c7f9d2e60687))
* **config:** explain a repeated plugin key instead of advising rta init ([fa7c5af](https://github.com/this-is-tobi/rule-them-all/commit/fa7c5af15ea596927758be970c049ce926ba97f6))
* **docs:** stop the boundary diagram clipping its own node text ([c4ccd44](https://github.com/this-is-tobi/rule-them-all/commit/c4ccd44dd08a942736a5d46109a2c3ea0cc69f11))
* **kv:** relabel an entry without re-supplying its secret ([43af6fa](https://github.com/this-is-tobi/rule-them-all/commit/43af6faa82e208aabc80a2486f16878041335024))

## 0.1.0 (2026-09-01)


### Features

* agent consent and policy infrastructure ([8ddf46e](https://github.com/this-is-tobi/rule-them-all/commit/8ddf46ebe1adbc13dd99b9a05ab600259166533a))
* **audit:** add kube.* compliance checks - RBAC, pod security, quotas, netpol ([ea4c647](https://github.com/this-is-tobi/rule-them-all/commit/ea4c6472c6a70ff1a9991cfe4247b8bed9b9592d))
* **audit:** narrow the kube.* audits to one namespace, honestly ([d448b63](https://github.com/this-is-tobi/rule-them-all/commit/d448b6364b34c181d44d221a658e5ad0d4c124f8))
* **cnpg:** complete contexts, namespaces and cluster names ([7752761](https://github.com/this-is-tobi/rule-them-all/commit/7752761c5c239641bf030685e398ea3cf7acd924))
* **cnpg:** deepen cnpg.status to what the resource actually says ([96bc7d8](https://github.com/this-is-tobi/rule-them-all/commit/96bc7d8f3387fb969e12e0f112df8d50307d0277))
* **kube:** add kube.event.list, keyed on how long a problem has run ([018da6c](https://github.com/this-is-tobi/rule-them-all/commit/018da6cd0c71c5a94a29f9394fe1355ffa8c7595))
* **kube:** add kube.metrics.pressure and kube.pvc.usage ([986a6db](https://github.com/this-is-tobi/rule-them-all/commit/986a6db600916e384900c8ceea161408a35db132))
* **kube:** add SRE diagnostics - quotas, PVCs, metrics, cert expiry ([3bcd3d1](https://github.com/this-is-tobi/rule-them-all/commit/3bcd3d129837636c20d9101cb3938fafd3218087))
* **kube:** mint a scoped ServiceAccount identity instead of the operator's own ([e3849e3](https://github.com/this-is-tobi/rule-them-all/commit/e3849e3451fb3d78f92bfb94308c289beff946e7))
* **kube:** report node health in the overview, and add kube.node.list ([c01de18](https://github.com/this-is-tobi/rule-them-all/commit/c01de18c5d9761426ca25af3e83d4a87d59a8de4))
* **mcp:** a locality gate for capabilities that describe this machine ([352ea77](https://github.com/this-is-tobi/rule-them-all/commit/352ea775fc68568ce3ea112de6e37ef53cc2d725))
* **mcp:** add OIDC as a second bearer-auth mechanism for --http ([89fc7da](https://github.com/this-is-tobi/rule-them-all/commit/89fc7dab756711af8022af6b64d4d32bae1b2b7f))
* **mcp:** serve over HTTP, bearer-authenticated, fail-closed ([45969fd](https://github.com/this-is-tobi/rule-them-all/commit/45969fd5b1e68193b30e42b0a5a7a500d54bba57))
* **pg:** restore what pg.dump writes, closing the backup chain ([2628b08](https://github.com/this-is-tobi/rule-them-all/commit/2628b0874effe3930f169b99919c1c1dc29e491b))
* **plugin:** add outdated, a cheap label check across installed plugins ([efdad2a](https://github.com/this-is-tobi/rule-them-all/commit/efdad2accf4228fb1fa00aa4bdeffbb2ceef1c97))
* **plugin:** add SecretSlice, so a repeatable credential can be declared ([913146a](https://github.com/this-is-tobi/rule-them-all/commit/913146aa4166538f9a9eaa342074e9e328c53338))
* **profile,plugin:** trust TLS across tunneled and direct connections ([817a021](https://github.com/this-is-tobi/rule-them-all/commit/817a021723477ba64faeeb86f7d7e445669479c4))
* profiles as environments, tunnels, plugin trust, and distribution begins ([2bf9f82](https://github.com/this-is-tobi/rule-them-all/commit/2bf9f826f7dbdfe23fc10da0956370ee51aae664))
* **release:** publish a Docker image and attest release binaries ([ad7e873](https://github.com/this-is-tobi/rule-them-all/commit/ad7e8730bc844bcc3ad8a8acb0f2e5246c0d906d))
* the capability model, three surfaces, and the first built-ins ([5ee1ca0](https://github.com/this-is-tobi/rule-them-all/commit/5ee1ca013314160928f4e1391bf507b4a526806e))
* the plugin catalogue grows, remote audit, release packaging, and observability ([ce2a50b](https://github.com/this-is-tobi/rule-them-all/commit/ce2a50bde4659dea1f9e8213b938a0175ee064ea))
* the plugin SDK and host, confinement hardening, and the first plugins ([92c3425](https://github.com/this-is-tobi/rule-them-all/commit/92c342552b5393edf820af89ee0710de51e06b94))
* **tui:** allow a plugin its credential locations from the pane that shows them ([043835e](https://github.com/this-is-tobi/rule-them-all/commit/043835e797016be68d89e0e4a026fb0a4f6fa93e))


### Bug Fixes

* **agent:** agent.allow now enforces the team's never/neverProfile ceiling ([0a9565e](https://github.com/this-is-tobi/rule-them-all/commit/0a9565ea66efb2cf05262b8c8a8e23044809e6eb))
* **app:** plugin allow no longer drops earlier grants it doesn't re-list ([18d1079](https://github.com/this-is-tobi/rule-them-all/commit/18d10791764c5aba9310287484c7fd1212b262da))
* **atomicfile,consent,grant,seal,agentlog,profile,plugintrust:** bound every read of rta's own state ([fca1e33](https://github.com/this-is-tobi/rule-them-all/commit/fca1e33c2ffdff53ec827f11603c431b8eaacc76))
* **audit:** a requirements.txt pin with no version no longer panics ([a8aadcc](https://github.com/this-is-tobi/rule-them-all/commit/a8aadccd7dd7962f983c011aa148d5b0394459f2))
* **audit:** scoped npm packages no longer parse to a name OSV can't match ([5dc82bf](https://github.com/this-is-tobi/rule-them-all/commit/5dc82bf8c2e5f4c8a6a06ea6e3732852aba7d45f))
* **build:** refuse a plugin directory name the shell would expand ([f5259e7](https://github.com/this-is-tobi/rule-them-all/commit/f5259e77eb17c75f2322ba18cd1c7b06b3c3d1ee))
* **cert:** require a grant before cert.expiry dials caller-chosen hosts ([6540dc0](https://github.com/this-is-tobi/rule-them-all/commit/6540dc004b8436717a31dc998447a4ef2cbea616))
* **clipboard:** bound the clipboard-program shell-out and reap what it forked ([01e40e5](https://github.com/this-is-tobi/rule-them-all/commit/01e40e57fa65221cb00dd38ed37a6689226e5889))
* **cnpg:** refuse --namespace and --all-namespaces together ([d736016](https://github.com/this-is-tobi/rule-them-all/commit/d736016994cc0a7e0d984ad324539e48a95fc976))
* **config,profile,pluginconf:** a namespace named twice is ambiguous, not first-match-wins ([2a2fc06](https://github.com/this-is-tobi/rule-them-all/commit/2a2fc06766eb64e81056cdc4e26310b5b93deba5))
* **config:** stop honouring plugins: and dashboard: from a file nobody named ([59f1d8b](https://github.com/this-is-tobi/rule-them-all/commit/59f1d8bcdaff3cb201363d1c6a0199336272ae31))
* **consent,agentlog,seal:** close the gaps in the record's own trust machinery ([1fe573d](https://github.com/this-is-tobi/rule-them-all/commit/1fe573d5f4c6684cf49522d1b3e95cb213b36524))
* **filelock:** close breakStale's renewal race and its restore gap ([9964e93](https://github.com/this-is-tobi/rule-them-all/commit/9964e93dd2bd423ce4a74a9364e4e45f49185b92))
* **gitclone:** cap how much object data one InMemory clone may store ([fb28ae9](https://github.com/this-is-tobi/rule-them-all/commit/fb28ae98aea3bd7c6586b7e4154244be47191fd4))
* **grant,agent:** apply the team's TTL ceiling everywhere a grant is issued ([7ae2479](https://github.com/this-is-tobi/rule-them-all/commit/7ae247932938d581562cd02499bcdc420ebb5ff6))
* **grant:** a rate-exhausted grant no longer masks an unrelated missing one ([e6c4f52](https://github.com/this-is-tobi/rule-them-all/commit/e6c4f5244e4b73e49669733544d8812c03e021df))
* **grant:** grant.list no longer hands any MCP caller the whole roster ([26bb3b7](https://github.com/this-is-tobi/rule-them-all/commit/26bb3b7b022006ea7790adc054954e411ec8afda))
* **grant:** spending an unrelated grant no longer deletes a ceiling-suppressed one ([e0527c9](https://github.com/this-is-tobi/rule-them-all/commit/e0527c981bad4fcfabe219afaf60fcf7138d22ef))
* **http:** block SSRF-adjacent addresses and flag truncated bodies ([36224df](https://github.com/this-is-tobi/rule-them-all/commit/36224df5d26aed2f3447353a70d5b3fdaa9b72c3))
* **kube,cnpg,tui:** close the classes the first pass only closed instances of ([237a807](https://github.com/this-is-tobi/rule-them-all/commit/237a80732cf532e8f4ec8b3f4e931f67b3a6ede9))
* **kube:** correct cert.list's false claim that it never requests tls.key ([cecdf7c](https://github.com/this-is-tobi/rule-them-all/commit/cecdf7ce79254bb033e8217aae6507da38aa0152))
* **kube:** keep namespace completion working when both namespace flags are set ([237a807](https://github.com/this-is-tobi/rule-them-all/commit/237a80732cf532e8f4ec8b3f4e931f67b3a6ede9))
* **kube:** namespace completion contacts the cluster, so mark it Live ([4f14173](https://github.com/this-is-tobi/rule-them-all/commit/4f1417381526658c4923e8f22c202bef54114973))
* **kube:** refuse --namespace and --all-namespaces together ([72bd202](https://github.com/this-is-tobi/rule-them-all/commit/72bd202946d537eb2b3c162dd406d6c9447751a9))
* **kube:** reject a provision --ttl below Kubernetes' own 10m token floor ([4e6ea10](https://github.com/this-is-tobi/rule-them-all/commit/4e6ea106badcbd3b84b0ce9dea267a09d1b66e4a))
* **kube:** stop counting completed Jobs as unhealthy pods ([ec4cef1](https://github.com/this-is-tobi/rule-them-all/commit/ec4cef194e00c31519fb4e763b80de2d18092441))
* **kube:** stop reporting a cluster's refusal as a cluster's silence ([4b68b10](https://github.com/this-is-tobi/rule-them-all/commit/4b68b10e968a3557aafebbeb9661e1d662dfc882))
* **kube:** write the minted kubeconfig atomically rather than truncating ([e509c16](https://github.com/this-is-tobi/rule-them-all/commit/e509c16ba3aced5becd3dfbde71a61e2f15854cc))
* **kv:** garbage in kv.recipients no longer locks out the real passphrase ([048ce8a](https://github.com/this-is-tobi/rule-them-all/commit/048ce8a8f9856182736f23705d49cbd849398166))
* **mcp,agent:** audit.deps, audit.why and kv.status describe this machine too ([9b23250](https://github.com/this-is-tobi/rule-them-all/commit/9b23250b40fa3457ea372b5a2c31dfa71fb3a24f))
* **mcp,app:** refuse a group-writable token file, and stop overclaiming ([f12e48e](https://github.com/this-is-tobi/rule-them-all/commit/f12e48e06880f6f8456c6af0b1f729049143cf68))
* **mcp:** a digest pin below 8 hex chars no longer authorizes anything ([d0936ae](https://github.com/this-is-tobi/rule-them-all/commit/d0936aea3d6713d26398fb6d55787be89f66c1d4))
* **mcp:** close the auth, startup and shutdown gaps an adversarial review found ([0d4fafb](https://github.com/this-is-tobi/rule-them-all/commit/0d4fafb4f64096af621da4232a3187336186622b))
* **mcp:** recover a panicking capability instead of taking the whole server down ([81c40d4](https://github.com/this-is-tobi/rule-them-all/commit/81c40d433c10bbfe1a67130fb9ca840753d51995))
* **net:** require a grant before net.probe/net.port reach a caller-chosen host ([9cedcec](https://github.com/this-is-tobi/rule-them-all/commit/9cedcecc0e2536747a40aacbfa19ca2f01cafbfa))
* **pathguard:** refuse a UNC path before resolving it ([4bf8e75](https://github.com/this-is-tobi/rule-them-all/commit/4bf8e754c51ba1e80903990489a0b9cc7c42a728))
* **pg:** stop authenticating from the operator's ambient ~/.pgpass ([28e9d9d](https://github.com/this-is-tobi/rule-them-all/commit/28e9d9d89d712bb74c61f6e744233b47c5ef3260))
* **plugin,profile:** distribution hardening — credential needs, index manifests, profile repair ([580c34d](https://github.com/this-is-tobi/rule-them-all/commit/580c34d78c1c290f12b966a21622a375db4d572d))
* **plugin:** cap identifier length and one capability's input/option count ([034f26c](https://github.com/this-is-tobi/rule-them-all/commit/034f26ca876ff4c5e9f948b2f3bd440f1024d112))
* **plugindist:** validate what git may clone, and confine file:// to local indexes ([b83afc0](https://github.com/this-is-tobi/rule-them-all/commit/b83afc0723f9d6bc8c36be6109508a4c4b29da95))
* **plugintrust:** refuse an ambiguous digest prefix instead of taking all of it ([f3f3814](https://github.com/this-is-tobi/rule-them-all/commit/f3f38149a12c61b9a65e68a5f113789d5e57fd56))
* **policy,plugindist:** guard every YAML decode against alias expansion ([722248b](https://github.com/this-is-tobi/rule-them-all/commit/722248b3507494738563f4bd32232375e5ad67d3))
* **policy:** stop hand-duplicating plugin.Namespace, which had drifted ([6da3ead](https://github.com/this-is-tobi/rule-them-all/commit/6da3ead556dd9461e8d6f13926aaae12653de0c8))
* **render:** close a markdown injection gap and add csv formula-injection defense ([ca1b5c6](https://github.com/this-is-tobi/rule-them-all/commit/ca1b5c6848ea118c3fc697e366e290073a00ca94))
* **render:** escape the markdown fields that only look like identifiers ([74162e3](https://github.com/this-is-tobi/rule-them-all/commit/74162e3752fdf8ca50fdc28922e8837c95d78ffc))
* **s3:** bound copy, rename and rm to the record their grant names ([4933cf1](https://github.com/this-is-tobi/rule-them-all/commit/4933cf1db53ec977fa5d21dc417c93fce21995fb))
* **sys:** report the CPU model on arm64 and the disk in a container ([a3dae6b](https://github.com/this-is-tobi/rule-them-all/commit/a3dae6b6e80d31eecf8746466e5b9c193eb69dab))
* **todo,note:** concurrent writes to the same store no longer lose one ([dedb45f](https://github.com/this-is-tobi/rule-them-all/commit/dedb45f929a898368f1da026dade3c4d1bab27ae))
* **toolcall:** enforce Options on Int, Float and Bool fields too ([7e5df92](https://github.com/this-is-tobi/rule-them-all/commit/7e5df92b019941399d72926e7b66bda05a831907))
* **tui,app:** say the real reason a plugin has no dashboard tile ([9aaee51](https://github.com/this-is-tobi/rule-them-all/commit/9aaee51beb7cc827ff8d86aef35ad3c6daa19014))
* **tui:** "e" no longer shadows edit-inputs, and duplicate tiles no longer swap results ([4fbc57e](https://github.com/this-is-tobi/rule-them-all/commit/4fbc57e911e15fef0568a6abf8caf73874ed57a6))
* **tui:** edit-inputs no longer reseeds a Secret field with the last run's plaintext ([0acf386](https://github.com/this-is-tobi/rule-them-all/commit/0acf386e1163ca0bf62db1be328d1267ea7f1743))
* **tui:** let the search bar delete words and clear the line ([fc93ebf](https://github.com/this-is-tobi/rule-them-all/commit/fc93ebf0ab39d90dca11092ff9243e44f81d4985))
* **tunnel:** a splice that loses the closing race no longer leaks pipe fds ([cda3818](https://github.com/this-is-tobi/rule-them-all/commit/cda3818c26b955a657a5426214f3764111c70429))
* **tunnel:** refuse a kube coordinate segment that begins with a dash ([f3308b5](https://github.com/this-is-tobi/rule-them-all/commit/f3308b56cd5c6159cfc1c1609470f723fbbcb415))

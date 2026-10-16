# Config examples

Pieces of a real environment, one need at a time, to copy into a file of your own and change. Each block is a drop-in for `config.d/` (see [your config file](./22-your-config-file.md)): put the ones you want into `~/.config/rta/config.d/shop.yaml`, then run `rta config check`, which names every line that is wrong and what to write instead. The blocks are parts of one file: each states `profiles:` or `roles:` or `dashboard:` on its own, so merge the entries under one heading, since a profile is stated in exactly one file.

A few things hold for all of them:

- **The names are made up.** `shop` is the environment, `web` and `billing` are its applications, `my-cluster` is a kubeconfig context and `shop-web` a namespace. Replace them with yours.
- **A plugin that is installed needs its digest.** Every entry below is written with the plugin's bare name, which is how a reader learns the shape. `rta config check` answers a bare one with the line to write (`kube@609e31ded3e4:`), and after a plugin upgrade `rta profile repin shop --plugin kube` rewrites it. [Profiles in depth](./41-profiles-in-depth.md) says why a profile names the build and not the version.
- **A line you state is a decision.** `set:` states a default a call may override, `only:` fences a value so a call cannot name another, and `secrets:` names where a credential is kept and never holds one. A commented line in a block is an option: take the `#` and the space off and it is valid.
- **Nothing here grants anything.** A profile says where a call goes; what an agent may do is [a grant](../30-boundary/35-roles-consent-and-the-guard.md), and the roles below only bundle the lines for you to issue.

## A cluster and its databases

Kubernetes, CloudNativePG clusters and the Postgres databases in them, for an environment with two applications. Every plugin that appears more than once is a labelled instance, `cnpg/web`, and `--profile shop/web` picks one.

```yaml
profiles:
  shop:
    note: shop, production
    dashboard: shop   # the screen drawn while shop is on (see the last section)
    # ttl: 8h         # `rta use shop` would lapse on its own after eight hours
    plugins:
      kube:
        set:
          context: my-cluster
          serviceaccount.ttl: 1h   # how long `rta kube serviceaccount provision` mints a token for
        # The fence: a call naming a namespace outside this list is refused, so is one naming none,
        # and --all-namespaces is refused. It holds what a call carries; a capability with no
        # namespace input (kube.overview, kube.node.list) reads what the credential allows.
        only:
          namespace: [shop-web, shop-billing]

      # `only: cluster:` names the cluster each instance reads and is what a call gets when it names
      # none. Unlike `set: cluster`, it leaves `cnpg backup request` to name its cluster on the call.
      cnpg/web:
        set: {context: my-cluster, namespace: shop-web}
        only: {cluster: [web-pg-cluster], namespace: [shop-web]}
      cnpg/billing:
        set: {context: my-cluster, namespace: shop-billing}
        only: {cluster: [billing-pg-cluster], namespace: [shop-billing]}

      # `kube:` is a `kubectl port-forward` rta opens for the one call: context/namespace/svc/service:port.
      # It fills host, port and sslmode. The service is one of CloudNativePG's three: -rw (primary),
      # -ro (replicas), -r (any instance). `secrets: password:` reads that Secret for each call and the
      # agent never receives it.
      pg/web:
        kube: my-cluster/shop-web/svc/web-pg-cluster-r:5432
        set: {database: web, user: web}
        only: {database: [web]}
        secrets: {password: "kube:web-pg-cluster-app/password"}
      pg/billing:
        kube: my-cluster/shop-billing/svc/billing-pg-cluster-r:5432
        set: {database: billing, user: billing}
        only: {database: [billing]}
        secrets: {password: "kube:billing-pg-cluster-app/password"}
      # A second connection to the same database through the replicas, so reads stay off the primary
      # and a grant can name this one without naming the other.
      pg/billing-replica:
        kube: my-cluster/shop-billing/svc/billing-pg-cluster-ro:5432
        set: {database: billing, user: billing}
        only: {database: [billing]}
        secrets: {password: "kube:billing-pg-cluster-app/password"}
```

With five instances and no bare one, `--profile shop` on a database read runs on all five, one section each, and on a write is refused with the list rather than guessed. The Secret is CloudNativePG's own `<cluster>-app`, and its owner and database are the application's name unless the cluster says otherwise.

## Services reached over the network

Vault, Keycloak and an S3 bucket. The credential of each is an entry of [rta's store](./50-secrets.md), so the file holds a name and never a value: `rta kv set shop-vault-token`, then press `U` in the TUI to unlock the store for the sitting, or start rta with the passphrase in its environment.

```yaml
profiles:
  shop:
    plugins:
      vault:
        set:
          address: https://vault.example.com
          kv-mount: secret   # the KV v2 engine's mount path
          # ca-file: ~/.config/shop/vault-ca.crt   # a PEM bundle to trust beyond the system's own; keep it outside ~/.config/rta
        secrets: {token: "kv:shop-vault-token"}
      keycloak:
        set:
          url: https://sso.example.com/   # the part before /realms
          realm: shop                     # the realm to read
          auth-realm: master              # the realm the client lives in, when it is not the one read
        secrets: {client-secret: "kv:shop-keycloak-client-secret"}
      s3:
        set:
          endpoint: s3.fr-par.scw.cloud   # host[:port], no scheme
          region: fr-par
          tls: true
          access-key: SCWXXXXXXXXXXXXXXXXX   # an identifier, not a secret
        # Fence the buckets this connection may reach; with one listed, a call that names none gets it.
        only: {bucket: [shop-backups]}
        secrets: {secret-key: "kv:shop-s3-secret-key"}
```

Keycloak signs in as the service account of a confidential client, `rta` unless `client-id` says otherwise, which needs the realm-management roles view-users, view-clients, view-realm, view-events and view-authorization. Give the S3 key only what it will be used for: an `s3` connection offers every S3 capability to a grant, deletes included, so a read-only key is the safer one.

## Versions to watch and tools to keep current

`eol` is not a connection: the versions you run or plan to run, graded against endoflife.date. `pkg` is the tools list this environment cares about, which replaces the machine's list while it is on.

```yaml
profiles:
  shop:
    plugins:
      eol:
        set:
          # An entry is `product`, `product@cycle` or a range `product@from..to`. A bare product lists
          # every cycle ever released, so pin what you run.
          products:
            - kubernetes@1.34..1.37
            - postgres@16..18
            - keycloak@26.7
          # warn-days: 90   # flag a cycle this many days before its end of life
      pkg:
        set:
          # <binary on $PATH>=github:<owner>/<repo>: rta runs `<binary> --version` and compares it with
          # the repository's latest release. At most thirty entries, one GitHub call each.
          tools:
            - kubectl-cnpg=github:cloudnative-pg/cloudnative-pg
            - kubectl-stern=github:stern/stern
```

## Roles for an agent

A role is a day of grants under one word. It grants nothing by being in the file: you issue it, at a terminal, with `rta grant issue shop-look --agent claude --dry-run` to rehearse. A line is a capability, an optional record (a namespace, a cluster) and flags, split on spaces. Name capabilities and never a bare plugin, which would cover every capability of it, writes included.

```yaml
roles:
  # Health and inventory: no row of a table and no secret value reaches the agent. kube.overview reads
  # every TLS Secret of the cluster into rta's own process on each call, so it carries a rate.
  shop-look:
    agent: claude
    ttl: 8h
    grants:
      - kube.overview --profile shop --rate 60/1h
      - kube.pod.list --profile shop
      - kube.event.list --profile shop
      - cnpg.status --profile shop/web
      - cnpg.status --profile shop/billing
      - pg.status --profile shop/web
      - pg.status --profile shop/billing

  # One namespace, narrowed by the record: the pods of shop-web and of nothing else.
  shop-web:
    agent: claude
    ttl: 2h
    grants:
      - kube.pod.list shop-web --profile shop
      - kube.event.list shop-web --profile shop

  # Rows: pg.query is read-only but hands over whatever the database holds, and no table can be named.
  # One hour, thirty calls an hour, on the replicas.
  shop-rows:
    agent: claude
    ttl: 1h
    grants:
      - pg.query --profile shop/billing-replica --rate 30/1h

  # A person-approved write that works once. The record is the cluster, which a call always names.
  shop-backup:
    agent: claude
    ttl: 30m
    grants:
      - cnpg.backup.request billing-pg-cluster --profile shop/billing --max-uses 1 --note before-upgrade
```

`rta grant issue --profile shop --agent claude` is a shortcut with no role, and it is far wider than any role above: every read of every connection of the environment, for twelve hours, with no rate limit. Count it before you run it.

## A dashboard by service type and by application

A screen is a whole arrangement of tiles, and while it is drawn it replaces your base `dashboard:` block, so whatever you still want from the base is restated. `V` in the TUI draws the next screen for the session. [Dashboard and theme](./25-dashboard-and-theme.md#views-a-screen-per-environment) has the keys of a tile.

```yaml
dashboard:
  views:
    # By service type.
    shop:
      sections:
        this machine: {collapsed: true}
      tiles:
        - {id: kube.overview, profile: shop, section: kubernetes}
        - {id: kube.metrics.node, profile: shop, section: kubernetes}
        - {id: cnpg.status, profile: shop, section: databases}   # one panel per cnpg connection
        - {id: eol.watch, profile: shop, section: versions}
        - {id: pkg.tools, profile: shop, section: versions}
        - {id: agent.overview, section: agents}
        - {id: grant.list, section: agents}
        - {id: sys.overview, section: this machine}
        - {id: net.overview, section: this machine}

    # By application: each one's pods and its database side by side.
    shop-apps:
      tiles:
        - {id: kube.pod.list, profile: shop, with: {namespace: shop-web}, section: web}
        - {id: cnpg.status, profile: shop/web, section: web}
        - {id: pg.status, profile: shop/web, section: web}
        - {id: kube.pod.list, profile: shop, with: {namespace: shop-billing}, section: billing}
        - {id: cnpg.status, profile: shop/billing, section: billing}
        - {id: pg.status, profile: shop/billing, section: billing}

    # The services this machine reaches over the network, by type.
    shop-remote:
      tiles:
        - {id: vault.overview, profile: shop, section: vault}
        - {id: keycloak.overview, profile: shop, section: keycloak}
        - {id: s3.overview, profile: shop, section: s3}
```

What a tile costs decides how many to draw:

- A drawn tile runs when its screen opens and again at its pace: every five seconds for a read of this machine, every thirty for one that reaches a connection, and slower where its capability says so (`kube.overview` a minute, `eol.watch` two hours, `pkg.tools` an hour). `refresh:` on a tile, a section or the screen states another, never under what a capability declared. `rta dashboard list --view shop` prints each tile's pace.
- A folded section still runs. Folding hides the panels and the header keeps saying the worst of them. What does not run is every tile of a screen that is not drawn, and every screen while a result page is open.
- A `pg` tile is two kubectl calls and a database login, a second or two, every thirty seconds, in Postgres's logs too. Behind a bastion that audits every Kubernetes request, each tile is audit events for as long as the screen is drawn. That is why the databases' tiles sit on the application screen and not on the first one.
- The vault, keycloak and s3 tiles need the store open: until you press `U` they say so.

## Next

[Seeing the shape of things](./30-trees.md) — mapping a directory, a bucket, a Vault mount or an etcd keyspace in one call.

# Public deployment by dispatch

`deploy-public` is a `workflow_dispatch` in this repository that runs on Almaz's
own Gitea runner (label `almaz`). It resolves a plan with
`scripts/deploy_plan.py` and hands it, over SSH, to a forced command on Almaz
that runs `scripts/public_deploy.py` as the deployment owner. Design and
decisions: [plan 0006](../plans/0006-automated-update-and-deploy.md).

It deploys frontend and API images only. Since 2026-11-14 the platform cores
(`archive-core`, `bill-core`, `democracy-core`) are rolled out by each
consumer's GitHub `deploy` workflow through its own fleet-deploy receiver
profile ([automatic core deploys](../reference/headless-core.md#automatic-core-deploys)).
No target lists a core, and the script refuses a configuration with a `core`
service, so the two paths cannot replace the same container.

## What a run does

1. The plan names, per compose service, the image that the owning repository
   published for its current `main` commit (`<registry>/<name>:<40-hex>`).
2. On Almaz the script takes `~/.local/state/arkhiv-deploy/deploy.lock` and,
   for the platform target, the maintenance lock the monitor, replay and backup
   jobs share. It pulls every image, refuses any whose
   `org.opencontainers.image.revision` label differs from the planned revision,
   and derives brand frontends that must proxy a differently named core
   (`democracy-core`, `bill-core`) from the consumer image.
3. If an API image changes, it runs the configured backup first.
4. Each changed service is swapped through a compose overlay that changes only
   the image: configuration equivalence is checked with `compose config`,
   `up --wait` runs for that one service, the new container must keep the old
   environment, host limits and mounts, and the public origin must answer 200
   with the expected body. Any failure restores that service's previous image
   and stops the run; earlier services stay on their new images and the receipt
   says so.
5. The configured `after` command (the public monitor) runs last.

Receipts, overlays and rollback overlays live under
`~/.local/state/arkhiv-deploy/deploys/<timestamp>-<target>/`. Previous images
are never deleted. `--dry-run` pulls, verifies and reports without swapping.

## Host installation (Almaz, as `onnwee`)

```sh
install -d -m 700 ~/.local/state/arkhiv-deploy
install -m 600 scripts/public_deploy.py ~/.local/state/arkhiv-deploy/public_deploy.py
install -m 600 deploy/public-deploy.example.json ~/.local/state/arkhiv-deploy/deploy.json   # then edit paths
install -m 700 deploy/arkhiv-deploy-entry.sh ~/.local/bin/arkhiv-deploy-entry
```

Add the deploy key to `~/.ssh/authorized_keys` with the forced command and no
other capabilities:

```
command="/home/onnwee/.local/bin/arkhiv-deploy-entry",restrict ssh-ed25519 AAAA... arkhiv-deploy
```

Store the private half as the repository secret `ALMAZ_DEPLOY_KEY`. The job
pins Almaz's host key from `deploy/almaz.known_hosts`. Rotate by replacing the
`authorized_keys` line and the secret together; the old key stops working at
once.

Updating the host script is a reviewed step: copy the merged revision of
`scripts/public_deploy.py` over the installed copy. Every receipt records the
SHA-256 of the script that ran.

## Rollback

Each receipt lists, per service, `rollback_command`: the same `compose up`
with that service's `*.rollback.json` overlay, which restores the previous
image id. Run it as `onnwee` on Almaz for the services to revert, newest first.
Receipts from runs before 2026-11-14 may also list core swaps and migrations.
Do not replay those core rollback commands: the fleet-deploy receiver now owns
each core, and its own receipts and rollback apply. Database migrations are
additive and checksum-locked; a core rollback must use a compatible checkpoint
when a migration has run, as [deployment operations](../DEPLOYMENT.md) already
requires.

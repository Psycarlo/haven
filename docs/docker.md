# Running Haven with Docker

The `Dockerfile` at the root builds an image with the relay, its pages and the example relay lists.

```bash
docker build -t haven .
docker run -d --name haven -p 3355:3355 \
  --env-file .env \
  -v haven-db:/app/db \
  -v haven-blossom:/app/blossom \
  -v "$PWD/whitelisted_npubs.json:/app/whitelisted_npubs.json:ro" \
  haven
```

- **Settings** come from the environment, as in [.env.example](../.env.example). The image holds no `.env`, so a missing required setting stops the relay at startup instead of falling back to an example value.
- **Data** lives in `/app/db` (the databases) and `/app/blossom` (media). Put both on volumes, or every new container starts empty.
- **Whitelist and blacklist** files are mounted wherever `WHITELISTED_NPUBS_FILE` and `BLACKLISTED_NPUBS_FILE` point, relative to `/app`. Changes apply within `LISTS_RELOAD_INTERVAL`, without a restart, as long as the container sees them: many editors save by replacing the file, which a single-file mount doesn't follow, so mount the folder that holds the lists instead.
- **Relay lists** for imports and blasting default to the examples. Mount your own and point `IMPORT_SEED_RELAYS_FILE` and `BLASTR_RELAYS_FILE` at them.
- **Commands** go after the image name: `docker run --rm --env-file .env -v haven-db:/app/db haven backup`.

`GET /health` answers `ok` once the private database answers a query, and the image's health check uses it.

The relay runs as the unprivileged user `haven` (uid 1000). The container starts as root only to hand that user the data folders, so volumes written by an image that ran as root keep working.

## Updating a relay on Coolify

[deploy/coolify-update.sh](../deploy/coolify-update.sh) moves a relay that Coolify runs from an inline Dockerfile to another
image, run on the server as root:

```bash
export COOLIFY_APP=<application uuid> COOLIFY_TOKEN=<API token with write and deploy rights>
./coolify-update.sh --rehearse ghcr.io/psycarlo/haven:<tag>   # every check, on a copy; the relay keeps running
./coolify-update.sh ghcr.io/psycarlo/haven:<tag>              # the update
```

The update stops the relay, saves its database folder, and has both the running image and the new one export every database
from their own copy. Only when the two exports are identical does it set the Dockerfile to `FROM <image>` and deploy. If the new
relay doesn't become healthy, `--rollback <run folder>` puts the previous Dockerfile back. Each run's backups stay in
`/root/haven-backups/<time>`; the last ten runs are kept.

It also turns on Coolify's consistent container name, so Coolify stops the old container before starting the new one. Both
database engines allow one process at a time, so the default rolling update, which starts the new container first, can't work.

---

[README](../README.md)

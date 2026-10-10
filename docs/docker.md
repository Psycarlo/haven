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
- **Whitelist and blacklist** files are mounted wherever `WHITELISTED_NPUBS_FILE` and `BLACKLISTED_NPUBS_FILE` point, relative to `/app`.
- **Relay lists** for imports and blasting default to the examples. Mount your own and point `IMPORT_SEED_RELAYS_FILE` and `BLASTR_RELAYS_FILE` at them.
- **Commands** go after the image name: `docker run --rm --env-file .env -v haven-db:/app/db haven backup`.

The relay runs as the unprivileged user `haven` (uid 1000). The container starts as root only to hand that user the data folders, so volumes written by an image that ran as root keep working.

---

[README](../README.md)

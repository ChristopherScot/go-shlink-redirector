# go-shlink-redirector

Fronts the `go.` short-URL host in homelab.

- `GET /{slug}` -> look up in shlink; if found, 302 to the long URL
- `GET /{slug}` (miss) -> 302 to the shlink web client's create-short-url page with `?customSlug={slug}` pre-filled
- `GET /health` -> 200 for probes

## Env

| var | required | default | notes |
|---|---|---|---|
| `SHLINK_API_URL` | yes | | e.g. `http://shlink.shlink.svc:8080` (no trailing slash needed) |
| `SHLINK_API_KEY` | yes | | shlink API key (from Vault via ESO in the k8s deploy) |
| `WEB_CLIENT_URL` | yes | | e.g. `https://shlink.home.chrisscotmartin.com` |
| `WEB_CLIENT_CREATE_PATH` | no | `/server/homelab/create-short-url` | web client create page path |
| `LISTEN_ADDR` | no | `0.0.0.0:3000` | listen address |

## Deploy

Image published as `ghcr.io/christopherscot/go-shlink-redirector:latest`. K8s manifests live in
[ChristopherScot/homelab](https://github.com/ChristopherScot/homelab) under `shlink/`.

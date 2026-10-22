# Examples

Small, runnable programs that build on the LANLeecher API. Create a token under
**Admin → API tokens** with the scope the example needs.

| Example | Language | Scope | What it does |
| --- | --- | --- | --- |
| [curl.sh](curl.sh) | shell | `read:rankings`, `read:clients` | the most common API calls |
| [obs-overlay/](obs-overlay/) | HTML/JS | `read:board` | top downloaders as a transparent OBS browser source |
| [discord-top10.py](discord-top10.py) | Python 3 (stdlib) | `read:board` | posts the top 10 to a Discord channel via webhook |
| [device-history/](device-history/) | Go | `read:clients` | download/upload of one device over all LANs |
| [grafana.md](grafana.md) | – | `read:rankings` | Prometheus scraping and Grafana panels |

All examples read the base URL and token from environment variables:

```bash
export LANLEECHER_URL=https://lanleecher.example
export LANLEECHER_TOKEN=llk_…
```

The API is described in [docs/api.md](../docs/api.md) and, completely, in the OpenAPI document at
`/api/openapi.json` of your instance.

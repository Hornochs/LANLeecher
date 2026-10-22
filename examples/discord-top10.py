#!/usr/bin/env python3
"""Post the current LANLeecher top 10 to a Discord channel.

Uses only the Python standard library. Create a webhook in Discord
(channel settings → Integrations → Webhooks) and a LANLeecher token with
scope read:board, then run it by hand or every hour from cron:

    export LANLEECHER_URL=https://lanleecher.example
    export LANLEECHER_TOKEN=llk_...
    export DISCORD_WEBHOOK=https://discord.com/api/webhooks/...
    python3 discord-top10.py
"""
import json
import os
import sys
import urllib.request


def fmt_bytes(v: float) -> str:
    for unit in ("B", "kB", "MB", "GB", "TB"):
        if v < 1000 or unit == "TB":
            return f"{v:.0f} {unit}" if unit == "B" else f"{v:.1f} {unit}"
        v /= 1000
    return ""


def get_board(base: str, token: str) -> dict:
    req = urllib.request.Request(f"{base}/api/v1/board?limit=10",
                                 headers={"Authorization": f"Bearer {token}"})
    with urllib.request.urlopen(req, timeout=10) as resp:
        return json.load(resp)


def lines(entries: list, field: str) -> str:
    medals = {1: "🥇", 2: "🥈", 3: "🥉"}
    out = []
    for e in entries:
        # Names come from the network: strip Discord markdown and mentions.
        name = e["name"].replace("@", "@\u200b").replace("`", "'").replace("*", "").replace("_", "")
        rank = medals.get(e["rank"], str(e["rank"]) + ".")
        out.append(f"{rank} **{name}** – {fmt_bytes(e[field])}")
    return "\n".join(out) or "–"


def main() -> int:
    base = os.environ["LANLEECHER_URL"].rstrip("/")
    board = get_board(base, os.environ["LANLEECHER_TOKEN"])
    if not board["event"]:
        print("no event running", file=sys.stderr)
        return 0
    totals = board["totals"]
    payload = {
        "username": "LANLeecher",
        "allowed_mentions": {"parse": []},
        "embeds": [{
            "title": f"Top 10 – {board['event']['name']}",
            "description": f"{totals['clients']} clients · {fmt_bytes(totals['bytes_down'])} down · "
                           f"{fmt_bytes(totals['bytes_up'])} up",
            "fields": [
                {"name": "Downloaders", "value": lines(board["top_download"], "bytes_down"), "inline": True},
                {"name": "Uploaders", "value": lines(board["top_upload"], "bytes_up"), "inline": True},
            ],
            "timestamp": board["generated_at"],
        }],
    }
    req = urllib.request.Request(os.environ["DISCORD_WEBHOOK"], data=json.dumps(payload).encode(),
                                 headers={"Content-Type": "application/json", "User-Agent": "LANLeecher"})
    urllib.request.urlopen(req, timeout=10).close()
    return 0


if __name__ == "__main__":
    sys.exit(main())

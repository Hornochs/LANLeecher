# Reverse proxy

LANLeecher speaks plain HTTP on `listen` (default `127.0.0.1:9040`). Put a TLS reverse proxy in
front of it and set in `config.yaml`:

```yaml
listen: 127.0.0.1:9040
public_url: https://lanleecher.example    # https:// marks session cookies Secure
trusted_proxies: [127.0.0.1, "::1"]        # the proxy's address, for real client IPs
```

Two things matter for every proxy:

1. **Client IP:** send `X-Real-IP` or `X-Forwarded-For`. LANLeecher uses it for login lockouts, rate
   limits and the audit log – but only from addresses in `trusted_proxies`.
2. **No buffering for `/api/v1/board/stream`.** The board uses Server-Sent Events; a buffering
   proxy delays updates until the buffer is full. LANLeecher sends `X-Accel-Buffering: no` and a
   keep-alive comment every 25 seconds, but some proxies need explicit configuration.

## nginx

```nginx
server {
    listen 443 ssl;
    http2 on;
    server_name lanleecher.example;
    ssl_certificate     /etc/ssl/lanleecher.crt;
    ssl_certificate_key /etc/ssl/lanleecher.key;

    location /api/v1/board/stream {
        proxy_pass http://127.0.0.1:9040;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
        proxy_buffering off;
        proxy_read_timeout 1h;
    }

    location / {
        proxy_pass http://127.0.0.1:9040;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
    }
}

server {
    listen 80;
    server_name lanleecher.example;
    return 301 https://$host$request_uri;
}
```

Run `nginx -t` before reloading.

## Caddy

Caddy handles TLS, client IP headers and streaming on its own:

```caddyfile
lanleecher.example {
    reverse_proxy 127.0.0.1:9040 {
        flush_interval -1
    }
}
```

## Traefik (Docker labels)

```yaml
labels:
  - traefik.enable=true
  - traefik.http.routers.lanleecher.rule=Host(`lanleecher.example`)
  - traefik.http.routers.lanleecher.tls=true
  - traefik.http.services.lanleecher.loadbalancer.server.port=9040
```

With `network_mode: host` (recommended for the resolvers), point Traefik at the host address
instead of a container port, and add the Traefik address to `trusted_proxies`.

## Without a proxy

At a LAN party without internet access, LANLeecher can serve directly:

- plain HTTP on the LAN: `listen: 0.0.0.0:9040`, `public_url: http://<ip>:9040` – cookies are then
  not marked Secure; acceptable only in a network you trust;
- built-in TLS with your own certificate:

```yaml
listen: 0.0.0.0:9443
public_url: https://lanleecher.lan:9443
tls:
  cert_file: /etc/lanleecher/tls.crt
  key_file: /etc/lanleecher/tls.key
```

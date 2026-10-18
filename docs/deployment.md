# Nginx and UFW on Linux

The optional `proxy` profile runs Nginx with `network_mode: host`, without Docker port publishing. Nginx listens on IPv4 port 38083 and forwards `/mcp`, `/health`, and `/ready` to Search MCP at `127.0.0.1:8081`. Other paths return 404. The Search MCP loopback binding remains unchanged; Meilisearch and SearXNG stay private.

This configuration provides HTTP for a trusted internal network. It does not provide TLS or client authentication. Use HTTPS with your organization certificate before sending traffic over an untrusted network. Native Linux Docker Engine is the deployment target.

## Prepare

Complete the README first-start preparation. Optional `.env` settings:

```dotenv
NGINX_PORT=38083
MCP_PORT=8081
```

`MCP_PORT` controls both the host-side Search MCP port and the Nginx upstream. `NGINX_PORT` must be a free port distinct from `MCP_PORT`. Nginx only binds IPv4; no IPv6 listener is configured. The template is rendered by the official Nginx image at startup. Buffering is disabled and the upstream read timeout is 300 seconds for MCP calls.

## Restrict access before starting

Inspect the existing host firewall first:

```sh
sudo ufw status verbose
sudo ufw status numbered
```

The policy must deny incoming connections by default, with no broad existing rule allowing the proxy port. Preserve required SSH/management access when configuring UFW. For an already enabled firewall with that policy, allow only your client IP (replace the example):

```sh
sudo ufw allow from 192.168.1.50 to any port 38083 proto tcp
```

Use your configured `NGINX_PORT` in every firewall rule and client URL. Do not open Search MCP port 8081. Host networking makes Nginx traffic subject to the host INPUT rules; publishing Nginx with Docker `ports` instead would reintroduce the Docker/UFW bypass.

## Validate and start

```sh
docker compose --profile proxy config --quiet
docker compose --profile proxy run --rm --no-deps nginx nginx -t
docker compose --profile proxy up --build -d
curl -fsS http://127.0.0.1:38083/ready
```

The MCP client endpoint is `http://<server-ip>:38083/mcp` using Streamable HTTP. From an allowed remote host, check `/ready` and MCP tool discovery/calls. From a disallowed remote host, confirm TCP 38083 is blocked. Localhost checks cannot validate remote UFW filtering.

```sh
docker compose --profile proxy logs -f nginx
docker compose --profile proxy stop nginx
```

After changing the template or port settings, recreate Nginx with `docker compose --profile proxy up -d --force-recreate nginx`.

## Automated proxy check

Run `python3 scripts/check-proxy.py`. It renders the Compose configuration, validates Nginx configuration and checks forwarding of MCP POST bodies/headers, readiness responses, and unknown-path rejection against a temporary fake upstream. It requires Docker, Python 3 and a locally available or downloadable Nginx image. It uses ephemeral loopback ports and removes its test container; it does not start the production stack or change UFW. This is proxy integration coverage, not a real Search MCP or remote firewall test.

## References

- [Docker host networking](https://docs.docker.com/engine/network/drivers/host/)
- [Docker and UFW](https://docs.docker.com/engine/network/packet-filtering-firewalls/)
- [Nginx proxy module](https://nginx.org/en/docs/http/ngx_http_proxy_module.html)

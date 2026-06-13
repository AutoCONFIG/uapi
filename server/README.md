# UAPI Split Deployment

This branch has two runtime roles:

```text
uapi-gateway = Gateway API + embedded web UI
uapi-relay   = execution node
```

There are only split Gateway and Relay runtimes.

## Layout

```text
server/all/              Single-host Gateway + Relay + PostgreSQL
server/gateway/          Gateway + PostgreSQL + embedded web
server/relay/relay-1/    Relay node 1
server/relay/relay-2/    Relay node 2
```

## Required edits

1. Replace all placeholder secrets before production use.
2. Create Relay Nodes in Gateway admin and replace each `gateway.relay_node_id`.
3. Set Relay Node `base_url` to the Relay domain, for example `https://relay-1.example.com`.
4. Bind channels to Relay Nodes in Gateway admin.

Gateway accepts optional remote debug dumps under `./debug-dumps`. Relay nodes are configured for remote dump mode and do not need a local dump volume.

`server/all` is still a split deployment: Gateway and Relay run as separate containers on one host. In Gateway admin, create a Relay Node with `base_url` set to `http://relay:8081` for this compose network.

## Start

Single host:

```bash
cd server/all
docker compose up -d
```

Gateway-only host:

```bash
cd server/gateway
docker compose up -d
```

Relay hosts:

```bash
cd server/relay/relay-1
docker compose up -d
```

```bash
cd server/relay/relay-2
docker compose up -d
```

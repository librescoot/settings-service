# Librescoot Settings Service

Part of the [Librescoot](https://librescoot.org/) open-source platform.

## Overview

`settings-service` is the persistent-settings bridge for a Librescoot vehicle. It seeds the volatile Redis/Valkey `settings` hash from `/data/settings.toml` at boot, exposes the installed settings schema, and persists user changes received through Redis.

## Capabilities

- Applies defaults from [`settings.schema.json`](settings.schema.json), then overlays values stored in `/data/settings.toml`.
- Publishes the raw schema at Redis string key `settings:schema` for clients that need setting metadata.
- Persists user-set, non-transient settings atomically to TOML; schema defaults alone are not written to the file.
- Applies `cellular.apn` to the existing NetworkManager WWAN connection and applies `scooter.logserver` to journal-upload configuration.
- Synchronizes `/data/wireguard/*.conf` with NetworkManager WireGuard connections. Changed configurations are re-imported, removed files remove their matching connections, and imported connections are configured to autoconnect indefinitely.
- Provides a persistent service-mode settings overlay.

## Operation and interfaces

### Settings contract

The service uses Redis/Valkey database 0 at `REDIS_ADDR` (default `localhost:6379`). Settings are flat fields in hash `settings`; a field name uses a section prefix and dot-separated nesting, for example `alarm.enabled` or `scooter.speed-limit`.

A writer must update the hash **and** notify channel `settings` with the field name. For example:

```sh
redis-cli HSET settings alarm.enabled false
redis-cli PUBLISH settings alarm.enabled
```

On boot the service writes the effective schema defaults and TOML overlay to the hash and publishes each written field. It sends systemd readiness only after that seed completes. The deployed unit therefore starts it before vehicle-service so consumers can observe the initial settings hash.

Only these TOML top-level sections are represented: `scooter`, `cellular`, `updates`, `dashboard`, `alarm`, `engine-ecu`, `keycard`, and `pm`. Values are converted to Redis strings. Consult [`settings.schema.json`](settings.schema.json) for supported keys, defaults, types, ranges, and transient markers; this service loads that metadata but does not itself reject a value based on the schema.

### Service-mode overlay

Commands are consumed from Redis list `settings:overlay` with `BRPOP`:

```sh
redis-cli LPUSH settings:overlay apply:service
redis-cli LPUSH settings:overlay clear:service
```

`apply:service` temporarily applies these settings: `scooter.auto-standby-seconds=0`, `pm.hibernation-timer=0`, `pm.default-state=run`, `alarm.enabled=false`, `scooter.usb0-policy=always-on`, `dashboard.mode=debug`, and `scooter.handlebar-unlocked=true`. It reports the state through `settings.dashboard.service-mode-active` and records whether the overlay was active in `/data/service-mode.json`. User edits to an overlaid field update the value to restore and the overlay value is reasserted until `clear:service`.

### Command-line options

```text
settings-service [--version]
  --settings-file PATH          defaults to /data/settings.toml
  --wireguard-config-dir PATH   defaults to /data/wireguard
  --schema PATH                 defaults to /usr/share/settings-service/settings.schema.json
```

## Configuration

`/data/settings.toml` is created when a persistable user-set setting is saved. A minimal example is:

```toml
[alarm]
enabled = true
honk = false

[cellular]
apn = "example.apn"
```

Transient schema keys remain in Redis and are removed from TOML when encountered. The service preserves the effective base value rather than its temporary overlay value when saving while service mode is active.

For APN management, NetworkManager and the target WWAN connection must be available. WireGuard configuration files must have a `.conf` suffix in `/data/wireguard`; SHA-256 sidecar files are maintained in the same directory.

## Build and test

Requires Go and the dependencies declared in `go.mod`.

```sh
make build       # static Linux ARMv7 binary: bin/settings-service
make build-host  # host binary: bin/settings-service-host
make test
make lint        # requires golangci-lint
```

`make fmt`, `make deps`, and `make clean` are also available.

## Deployment and runtime dependencies

The Yocto recipe installs the binary at `/usr/bin/settings-service`, the schema at `/usr/share/settings-service/settings.schema.json`, and systemd unit `librescoot-settings.service`. The unit requires `valkey.service`, requires `/data` to be mounted, wants `NetworkManager.service`, and runs as root.

Runtime dependencies are Redis/Valkey, writable `/data`, and—when APN or WireGuard features are used—NetworkManager with `nmcli`. The program accepts `SIGINT` and `SIGTERM` for graceful shutdown and emits `READY=1` when launched with a systemd notification socket.

## Operational and security notes

- Treat `/data/settings.toml` and `/data/wireguard/` as configuration that can affect vehicle behaviour and network access. Restrict write access to trusted administrators and services.
- Redis has no authentication or TLS configuration in this service. Keep the Redis/Valkey listener on a trusted local network and control access to clients that can modify `settings` or push overlay commands.
- A change notification without a matching hash update can cause the service to persist the current hash value; update the hash first.
- WireGuard imports and NetworkManager changes are performed asynchronously or best-effort and failures are logged. Review the journal after configuration changes.

## License

This project is licensed under the [Creative Commons Attribution-NonCommercial-ShareAlike 4.0 International License](LICENSE).

Made with ❤️ by the Librescoot community

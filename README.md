# Librescoot Settings Service

A Go service that synchronizes settings between Redis and a TOML configuration file.

Part of the [Librescoot](https://librescoot.org/) open-source platform.

## Usage

The service monitors Redis for changes and maintains settings in `/data/settings.toml` with the following structure:

```toml
[scooter]
speed_limit = "25"
mode = "eco"

[cellular]
apn = "internet.provider.com"
```

## Redis Commands

Settings must be prefixed with their section name:

```bash
# Set scooter settings
HSET settings scooter.speed_limit "25"
PUBLISH settings scooter.speed_limit

HSET settings scooter.mode "eco"
PUBLISH settings scooter.mode

# Set cellular settings
HSET settings cellular.apn "internet.lebara.de"
PUBLISH settings cellular.apn
```

## Special Features

- **APN Management**: When `cellular.apn` is updated, the service automatically updates `/etc/NetworkManager/system-connections/wwan.nmconnection` if it exists
- **Startup Sync**: On startup, the service reads `/data/settings.toml` and populates Redis after flushing existing settings
- **Empty Config Handling**: If the TOML file doesn't exist or has an empty `[scooter]` section, Redis settings are flushed
- **WireGuard Management**: On startup, the service:
  - Deletes all existing WireGuard connections from NetworkManager
  - Waits 120 seconds
  - Imports all `*.conf` files from `/data/wireguard/` as new WireGuard connections

## Environment Variables

- `REDIS_ADDR`: Redis server address (default: `localhost:6379`)

## Building

```bash
make build       # Build for ARM7
make build-amd64 # Build for AMD64
```

## License

This project is dual-licensed. The source code is available under the
[Creative Commons Attribution-NonCommercial-ShareAlike 4.0 International License][cc-by-nc-sa].
The maintainers reserve the right to grant separate licenses for commercial distribution; please contact the maintainers to discuss commercial licensing.

[![CC BY-NC-SA 4.0][cc-by-nc-sa-image]][cc-by-nc-sa]

[cc-by-nc-sa]: http://creativecommons.org/licenses/by-nc-sa/4.0/
[cc-by-nc-sa-image]: https://licensebuttons.net/l/by-nc-sa/4.0/88x31.png

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

---

Made with ❤️ by the Librescoot community


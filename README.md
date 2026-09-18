# pve-wol

轻量的 Proxmox VE Wake-on-LAN Gateway。程序通过 PVE HTTPS REST API 枚举有权限访问的 QEMU/LXC 及其全部网卡 MAC，在 UDP 7/9 收到标准 Magic Packet 后启动对应 Guest。

## 编译

需要 Go 1.22 或更新版本：

```sh
go mod download
make
```

生成的 `pve-wol` 是单个可执行文件。

## 安装

```sh
install -Dm755 pve-wol /usr/local/bin/pve-wol
install -Dm644 deploy/pve-wol.service /etc/systemd/system/pve-wol.service
install -d -m 700 /etc/pve-wol
install -m 600 config.example.yaml /etc/pve-wol/config.yaml
```

编辑 `/etc/pve-wol/config.yaml`，填写实际的 PVE URL、API Token ID 和 Secret。之后：

```sh
systemctl daemon-reload
systemctl enable --now pve-wol
journalctl -u pve-wol -f
```

普通 WoL 工具可以直接发送目标 MAC，例如：

```sh
wakeonlan BC:24:11:AA:BB:CC
```

网络必须能够把 UDP 广播送到 LXC；程序本身不会修改 PVE 宿主机、桥接或防火墙配置。

## 行为与限制

- 启动时同步一次，之后按 `sync_interval` 周期同步；未知 MAC 会触发一次即时同步。
- 每次处理已知 WoL target 时都会通过 PVE 查询 Guest 的实时运行状态，不使用周期同步中的缓存状态作为最终判断。
- Guest 迁移导致缓存 Node 失效时会刷新 Registry，并在同一次 WoL 请求中最多恢复重试一次。
- PVE Template 会被自动忽略，不会读取配置、提取 MAC 或加入 WoL Registry。
- 同一 MAC 默认 5 秒内只处理一次。
- 重复 MAC 会记录 warning，并为安全起见拒绝启动该 MAC 对应的 Guest。
- 仅支持 UDP 标准 Magic Packet；不支持 SecureOn、原始以太网 `0x0842`、IPv6 WoL 或自定义协议。
- `insecure_skip_verify: true` 仅用于兼容 PVE 默认自签名证书；API Token Secret 不会写入日志。

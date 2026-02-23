# Stratix Server

[![Release](https://img.shields.io/badge/version-0.0.1-blue.svg)](https://github.com/hotjp/stratix-server/releases/tag/v0.0.1)
[![Go](https://img.shields.io/badge/Go-1.21+-00ADD8.svg)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

超大规模分布式 WebSocket 网关，支持 10万+ Electron 客户端连接到数万至百万个 OpenClaw Gateway 实例。

## 架构设计

```
┌─────────────────────────────────────────────────────────┐
│                   Nginx/TCP Load Balancer                │
└─────────────────────────────────────────────────────────┘
                        │
        ┌───────────────┴───────────────┐
        │                               │
┌───────▼───────┐               ┌───────▼───────┐
│ Stratix GW #1 │      ...      │ Stratix GW #N │
└───────┬───────┘               └───────┬───────┘
        │                               │
        └───────────────┬───────────────┘
                        │
        ┌───────────────┼───────────────┐
        │               │               │
┌───────▼───────┐ ┌──────▼──────┐ ┌───────▼───────┐
│ 10k Clients  │ │ 10k Clients│ │ 10k Clients  │
└───────────────┘ └─────────────┘ └───────────────┘
```

## 核心特性

- **高性能连接管理**：Go 原生 net 包 + epoll 封装，单实例支持 10 万连接
- **智能路由**：基于 ClientID 前缀的路由规则，支持百万级目标网关
- **消息可靠性**：内存环形缓冲区 + 本地文件持久化 + 重试机制
- **速率限制**：内置令牌桶限流，可配置 QPS 和突发流量
- **消息历史**：支持客户端消息历史存储和回放
- **内存管理**：自动 GC 优化，可配置内存上限
- **轻量部署**：单二进制文件，无依赖，内存占用 ~100MB（10 万连接）
- **实时监控**：内置 metrics 接口，支持 Prometheus 集成

## 快速开始

### 1. 克隆项目

```bash
git clone https://github.com/hotjp/stratix-server.git
cd stratix-server
go mod download
```

### 2. 修改配置

编辑 `config/route.json`：

```json
{
  "gateway": {
    "listen": ":8080",
    "maxConnections": 100000,
    "connectionTimeout": "90s",
    "heartbeatInterval": "15s",
    "maxMessageSize": 52428800,
    "rateLimit": {
      "enabled": true,
      "requestsPerSecond": 1000,
      "burstSize": 2000
    },
    "history": {
      "enabled": true,
      "maxPerClient": 500
    }
  },
  "routes": [
    {
      "clientIdPrefix": "client-1-1000",
      "openclawGateway": "ws://gateway1.example.com:8080/ws"
    }
  ],
  "buffer": {
    "size": 1000000,
    "persistFile": "data/buffer.log"
  },
  "memory": {
    "maxMemoryMB": 2048,
    "gcInterval": "30s"
  }
}
```

### 3. 编译运行

```bash
# 编译
go build -o stratix-server cmd/main.go

# 运行
./stratix-server

# 或使用脚本
bash scripts/build.sh
bash scripts/start.sh
```

### 4. 测试连接

```bash
# 使用 wscat 测试
wscat -c "ws://localhost:8080/ws?clientId=client-123"

# 发送消息
{"type": 0, "messageId": "msg-001", "data": "Hello World"}
```

## 性能指标

| 指标 | 数值 |
|------|------|
| 单实例最大连接 | 100,000 |
| 单连接内存占用 | ~1KB |
| 消息延迟 | < 10ms |
| 吞吐量 | 100K QPS/实例 |
| 缓冲区大小 | 1,000,000 消息 |

## API 接口

### WebSocket 连接

```
ws://gateway:8080/ws?clientId={clientId}
```

### 消息格式

```json
{
  "type": 0,
  "clientId": "client-123",
  "messageId": "msg-001",
  "timestamp": 1709078400000000000,
  "data": "base64 encoded message"
}
```

### Metrics 接口

```
GET /metrics
```

响应：
```json
{
  "connectionsTotal": 50000,
  "connectionsActive": 49500,
  "messagesReceived": 1000000,
  "messagesSent": 999950,
  "messagesFailed": 50,
  "bytesReceived": 500000000,
  "bytesSent": 499975000,
  "uptimeSeconds": 3600
}
```

## 生产部署

### 1. 负载均衡

```nginx
upstream stratix_gateways {
    least_conn;
    server gw1.example.com:8080;
    server gw2.example.com:8080;
    server gw3.example.com:8080;
}

server {
    listen 443 ssl;
    server_name gateway.example.com;
    
    ssl_certificate /path/to/cert.pem;
    ssl_certificate_key /path/to/key.pem;
    
    location / {
        proxy_pass http://stratix_gateways;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
```

### 2. 系统调优

```bash
# 增加文件描述符限制
echo "* soft nofile 1000000" >> /etc/security/limits.conf
echo "* hard nofile 1000000" >> /etc/security/limits.conf

# 优化 TCP 参数
echo "net.core.somaxconn = 65535" >> /etc/sysctl.conf
echo "net.ipv4.tcp_max_syn_backlog = 8192" >> /etc/sysctl.conf
echo "net.ipv4.tcp_tw_reuse = 1" >> /etc/sysctl.conf
sysctl -p
```

### 3. 使用 systemd

```ini
[Unit]
Description=Stratix Server
After=network.target

[Service]
Type=simple
User=nobody
ExecStart=/usr/local/bin/stratix-server
WorkingDirectory=/var/lib/stratix-server
Restart=always
RestartSec=5
LimitNOFILE=1000000

[Install]
WantedBy=multi-user.target
```

## 相关项目

- [openclaw-stratix-plugin](https://github.com/hotjp/openclaw-stratix-plugin) - OpenClaw 集成插件

## License

MIT

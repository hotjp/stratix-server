# Stratix Gateway 架构设计文档

## 1. 规模分析

| 维度 | 数值 |
|------|------|
| Electron 客户端 | 1,000 - 100,000 |
| OpenClaw Gateway 目标 | 10,000 - 1,000,000 |
| 单用户 QPS | 100 |
| 总峰值 QPS | 100,000 - 10,000,000 |
| 单实例连接容量 | 100,000 |

## 2. 核心架构

### 2.1 分层设计

```
┌─────────────────────────────────────────────────────┐
│                    Nginx 负载均衡                     │
└─────────────────────────────────────────────────────┘
                        │
        ┌───────────────┼───────────────┐
        │               │               │
┌───────▼───────┐ ┌──────▼──────┐ ┌───────▼───────┐
│ Stratix GW #1 │ │ Stratix GW#2│ │ Stratix GW #N │
│ 10k Conns    │ │ 10k Conns  │ │ 10k Conns    │
└───────┬───────┘ └──────┬──────┘ └───────┬───────┘
        │               │               │
        └───────────────┴───────────────┘
                        │
         消息路由到 10k-1M OpenClaw Gateway
```

### 2.2 核心模块

| 模块 | 职责 | 技术选型 |
|------|------|---------|
| 连接管理 | WebSocket 连接池、心跳检测 | Go net + epoll |
| 路由模块 | ClientID → OpenClaw Gateway 映射 | 哈希表 + 前缀匹配 |
| 缓冲模块 | 消息队列、持久化 | 环形缓冲区 + 文件 |
| 监控模块 | 实时指标、日志 | atomic + 定时输出 |

## 3. 关键技术选型

### 3.1 轻量 vs 复杂方案对比

| 需求 | 轻量方案 | 放弃的复杂方案 |
|------|---------|---------------|
| 连接管理 | Go 原生 net + 连接池 | 自研 Reactor / io_uring |
| 路由管理 | JSON 配置 + 哈希表 | ETCD/ZooKeeper 分布式配置 |
| 消息可靠性 | 环形缓冲区 + 本地文件 | RocksDB/Redis Stream |
| 监控运维 | 控制台 + 日志 | Prometheus/Grafana |
| 集群扩展 | Nginx 负载均衡 | 自动发现/分片 |

### 3.2 为什么选择这些方案？

1. **Go net + epoll**
   - 性能：10 万连接 < 100MB 内存
   - 开发量：200 行核心代码
   - 成熟度：生产验证多年

2. **JSON 配置**
   - 简单：易于修改和热加载
   - 快速：内存哈希表 O(1) 查询
   - 可扩展：后续可升级到 ETCD

3. **环形缓冲区**
   - 无依赖：纯内存 + 文件
   - 高效：固定大小，无需 GC 压力
   - 可靠：文件追加，断电不丢失

## 4. 消息流程

### 4.1 客户端 → OpenClaw Gateway

```
1. Electron 客户端
   ↓ WebSocket 连接
2. Stratix Gateway 接收
   ↓ 查找路由
3. 路由模块
   ↓ 映射到目标 OpenClaw Gateway
4. 缓冲模块（暂存）
   ↓ 重试机制
5. 发送到 OpenClaw Gateway
```

### 4.2 消息格式

```json
{
  "type": 0,              // 0: Text, 1: Binary, 2: Control
  "clientId": "client-123",
  "messageId": "msg-uuid",
  "timestamp": 1709078400000000000,
  "data": "base64 encoded",
  "route": "ws://target-gateway:8080/ws"
}
```

## 5. 性能优化

### 5.1 内存优化

| 策略 | 效果 |
|------|------|
| 连接复用 | 减少频繁创建 |
| 缓冲池 | 减少分配 |
| 对象复用 | 减少 GC 压力 |

### 5.2 并发优化

```go
// 读写分离
conn.StartRead(handler)  // 独立 goroutine
conn.StartWrite()         // 独立 goroutine

// 无锁计数
atomic.AddInt64(&metrics.messagesReceived, 1)

// 连接表读写锁
clientsMu.RLock()
defer clientsMu.RUnlock()
```

### 5.3 网络优化

| 优化项 | 配置 |
|--------|------|
| TCP Keepalive | 15s |
| Read Buffer | 1KB |
| Write Buffer | 1KB |
| Heartbeat | 30s Ping |

## 6. 可靠性保障

### 6.1 消息不丢失

```
内存缓冲区（1M 消息）
    ↓
本地文件追加（异步）
    ↓
重试机制（3 次）
    ↓
发送确认
```

### 6.2 连接保活

- WebSocket Ping/Pong（30s）
- 连接超时检测（30s）
- 自动重连（指数退避）

### 6.3 优雅降级

| 场景 | 策略 |
|------|------|
| 目标网关不可达 | 消息入缓冲区 |
| 缓冲区满 | 丢弃最旧消息 |
| 连接数超限 | 503 错误 |

## 7. 监控与运维

### 7.1 核心指标

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

### 7.2 日志输出

```
INFO Client connected: clientId=client-123
INFO Route matched: clientId=client-123, rule=client-1-1000
WARN No route found: clientId=unknown-999
ERROR Write error: connId=client-456, err=broken pipe
INFO Gateway metrics: connections=49500, messagesRcv=1000000
```

## 8. 扩展计划

### Phase 1（当前）
- ✅ 单实例网关
- ✅ WebSocket 连接
- ✅ 路由转发
- ✅ 基础监控

### Phase 2（Q2）
- [ ] HTTP REST API
- [ ] 消息签名验证
- [ ] 速率限制
- [ ] 动态配置热加载

### Phase 3（Q3）
- [ ] Prometheus 集成
- [ ] Redis Stream 持久化
- [ ] 消息优先级

### Phase 4（Q4）
- [ ] 集群自动发现
- [ ] ETCD 配置中心
- [ ] 多区域容灾

## 9. 部署拓扑

### 9.1 小规模（1k-10k 客户端）

```
Nginx: 1 实例
Stratix GW: 1-2 实例
```

### 9.2 中规模（10k-50k 客户端）

```
Nginx: 2 实例（HAProxy）
Stratix GW: 5-10 实例
```

### 9.3 大规模（50k-100k 客户端）

```
Nginx: 4 实例（L4 LB）
Stratix GW: 10-20 实例
Prometheus + Grafana 监控
```

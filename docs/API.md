# Stratix Gateway API 文档

## 概述

Stratix Gateway 是一个高性能 WebSocket 网关，支持 10万+ 并发连接，用于 Electron 客户端与 OpenClaw Gateway 之间的消息转发。

**基础地址**: `ws://host:8080`

---

## 接口列表

| 接口 | 方法 | 说明 |
|------|------|------|
| `/ws` | WebSocket | 主连接接口 |
| `/health` | GET | 健康检查 |
| `/metrics` | GET | 监控指标 |

---

## 1. WebSocket 连接

### 连接地址

```
ws://{host}:8080/ws?clientId={clientId}[&loadHistory=true][&lastMessageId={id}][&limit={n}]
```

### 参数说明

| 参数 | 必填 | 类型 | 说明 |
|------|------|------|------|
| `clientId` | 是 | string | 客户端唯一标识 |
| `loadHistory` | 否 | boolean | 是否加载历史消息，默认 false |
| `lastMessageId` | 否 | string | 增量加载时的起始消息ID（不包含） |
| `limit` | 否 | int | 加载历史消息数量，默认 100 |

### 连接示例

```javascript
// 基础连接
const ws = new WebSocket('ws://localhost:8080/ws?clientId=user-123');

// 连接并加载历史
const ws = new WebSocket('ws://localhost:8080/ws?clientId=user-123&loadHistory=true&limit=50');

// 增量加载
const ws = new WebSocket('ws://localhost:8080/ws?clientId=user-123&loadHistory=true&lastMessageId=msg-100');
```

### 错误响应

| 状态码 | 说明 |
|--------|------|
| 400 | 缺少 clientId |
| 429 | 请求频率超限 |
| 503 | 连接数已达上限 |

---

## 2. 消息格式

### 发送消息

```json
{
  "type": 0,
  "messageId": "msg-uuid-123",
  "data": "消息内容或 base64 编码的数据",
  "contentType": "text/plain"
}
```

### 字段说明

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `type` | int | 是 | 消息类型：0=文本, 1=二进制, 2=控制 |
| `messageId` | string | 是 | 消息唯一标识，用于历史加载 |
| `data` | string/bytes | 是 | 消息内容 |
| `contentType` | string | 否 | 内容类型，如 `text/plain`, `image/png` |
| `compressed` | bool | 否 | 是否压缩 |

### 接收消息

```json
{
  "type": 0,
  "clientId": "user-123",
  "messageId": "msg-uuid-456",
  "timestamp": 1709078400000000000,
  "data": "响应内容",
  "contentType": "text/plain"
}
```

---

## 3. 历史消息

### 历史消息响应

连接时设置 `loadHistory=true`，会收到历史消息：

```json
{
  "type": 0,
  "messageId": "msg-100",
  "clientId": "user-123",
  "data": "历史消息内容",
  "timestamp": 1709078400000000000
}
```

### 历史加载完成通知

```json
{
  "type": 2,
  "action": "history_complete",
  "total": 50,
  "oldestMessageId": "msg-50",
  "newestMessageId": "msg-99"
}
```

### 历史配置

| 配置项 | 默认值 | 说明 |
|--------|--------|------|
| 每客户端保留条数 | 500 | 可配置 |
| 单条消息上限 | 64KB | 超过只存元数据 |
| 保留时间 | 24小时 | 自动清理 |

---

## 4. 大文件传输（分片）

对于超过 1MB 的二进制数据，使用分片传输。

### 分片协议

```
┌─────────────────────────────────────────────────────────┐
│ ChunkStart: 开始分片 (Binary)                           │
│ [0x01][JSON: {messageId, totalSize, chunkCount, ...}]   │
├─────────────────────────────────────────────────────────┤
│ ChunkData: 分片数据 (Binary)                            │
│ [0x02][4B index][4B hash][4B length][data...]           │
├─────────────────────────────────────────────────────────┤
│ ChunkEnd: 结束分片 (Binary)                             │
│ [0x03][JSON: {messageId, checksum}]                     │
└─────────────────────────────────────────────────────────┘
```

### 发送示例

```javascript
// 小于 1MB：直接发送
ws.send(JSON.stringify({
  type: 1,
  messageId: 'msg-123',
  data: base64Image,
  contentType: 'image/png'
}));

// 大于 1MB：使用分片（需客户端自行实现分片逻辑）
```

---

## 5. 健康检查

### 请求

```
GET /health
```

### 响应

```json
{
  "status": "ok"
}
```

---

## 6. 监控指标

### 请求

```
GET /metrics
```

### 响应

```json
{
  "connectionsTotal": 5000,
  "connectionsActive": 4950,
  "messagesReceived": 1000000,
  "messagesSent": 999950,
  "messagesFailed": 50,
  "bytesReceived": 500000000,
  "bytesSent": 499975000,
  "largeMessages": 100,
  "largeMessageBytes": 52428800,
  "rateLimitExceeded": 15,
  "currentConnections": 4950,
  "openclawConnections": 10,
  "memoryUsedMB": 512,
  "memoryLimitMB": 2048,
  "memoryPercent": 25,
  "heapAllocMB": 256,
  "heapSysMB": 512,
  "numGoroutine": 150,
  "rateLimitClients": 120,
  "rateLimitAvgTokens": 850,
  "rateLimitQPS": 1000,
  "historyClients": 1000,
  "historyMessages": 45000,
  "uptimeSeconds": 86400
}
```

### 指标说明

| 指标 | 说明 |
|------|------|
| `connectionsTotal` | 累计连接数 |
| `connectionsActive` | 当前活跃连接数 |
| `messagesReceived` | 收到消息数 |
| `messagesSent` | 发送消息数 |
| `messagesFailed` | 失败消息数 |
| `bytesReceived` | 收到字节数 |
| `bytesSent` | 发送字节数 |
| `largeMessages` | 大消息数（>1MB） |
| `rateLimitExceeded` | 限流触发次数 |
| `memoryUsedMB` | 内存使用（MB） |
| `memoryLimitMB` | 内存限制（MB） |
| `historyClients` | 有历史记录的客户端数 |
| `historyMessages` | 历史消息总数 |
| `uptimeSeconds` | 运行时间（秒） |

---

## 7. 连接限制

| 限制项 | 默认值 | 说明 |
|--------|--------|------|
| 最大连接数 | 100,000 | 单实例 |
| 消息大小限制 | 50MB | 单条消息 |
| 速率限制 | 1000 QPS/IP | 可配置突发 2000 |
| 连接超时 | 90秒 | 无活动断开 |
| 心跳间隔 | 15秒 | Ping/Pong |

---

## 8. 错误码

| 错误码 | 说明 |
|--------|------|
| 400 | 参数错误 |
| 429 | 请求频率超限 |
| 503 | 服务不可用（连接数满） |

### WebSocket Close Codes

| Code | 说明 |
|------|------|
| 1000 | 正常关闭 |
| 1001 | 客户端离开 |
| 1006 | 异常断开 |
| 1009 | 消息过大 |

---

## 9. 完整示例

### JavaScript 客户端

```javascript
class StratixClient {
  constructor(clientId, gatewayUrl) {
    this.clientId = clientId;
    this.gatewayUrl = gatewayUrl;
    this.ws = null;
  }

  connect(options = {}) {
    const params = new URLSearchParams({
      clientId: this.clientId,
      loadHistory: options.loadHistory || false,
      lastMessageId: options.lastMessageId || '',
      limit: options.limit || 100
    });

    this.ws = new WebSocket(`${this.gatewayUrl}/ws?${params}`);

    this.ws.onopen = () => {
      console.log('Connected');
      if (options.onOpen) options.onOpen();
    };

    this.ws.onmessage = (event) => {
      const msg = JSON.parse(event.data);
      
      // 历史加载完成
      if (msg.type === 2 && msg.action === 'history_complete') {
        console.log(`History loaded: ${msg.total} messages`);
        if (options.onHistoryComplete) options.onHistoryComplete(msg);
        return;
      }

      // 普通消息
      if (options.onMessage) options.onMessage(msg);
    };

    this.ws.onerror = (error) => {
      console.error('WebSocket error:', error);
      if (options.onError) options.onError(error);
    };

    this.ws.onclose = (event) => {
      console.log('Disconnected:', event.code);
      if (options.onClose) options.onClose(event);
    };
  }

  send(messageId, data, contentType = 'text/plain') {
    const msg = {
      type: 0,
      messageId: messageId,
      data: data,
      contentType: contentType
    };
    this.ws.send(JSON.stringify(msg));
  }

  close() {
    if (this.ws) {
      this.ws.close(1000);
    }
  }
}

// 使用示例
const client = new StratixClient('user-123', 'ws://localhost:8080');

client.connect({
  loadHistory: true,
  limit: 50,
  onOpen: () => {
    console.log('Connected to gateway');
    client.send('msg-001', 'Hello World');
  },
  onMessage: (msg) => {
    console.log('Received:', msg);
  },
  onHistoryComplete: (info) => {
    console.log('History loaded:', info);
  },
  onClose: (event) => {
    console.log('Disconnected');
  }
});
```

---

## 10. 配置参考

```json
{
  "gateway": {
    "listen": ":8080",
    "maxConnections": 100000,
    "connectionTimeout": "90s",
    "heartbeatInterval": "15s",
    "maxMessageSize": 52428800,
    "readBufferSize": 65536,
    "writeBufferSize": 65536,
    "rateLimit": {
      "enabled": true,
      "requestsPerSecond": 1000,
      "burstSize": 2000
    },
    "security": {
      "allowedOrigins": [],
      "enableCompression": true
    },
    "history": {
      "enabled": true,
      "maxPerClient": 500,
      "maxMsgSizeKB": 64,
      "cleanupHours": 24
    }
  },
  "memory": {
    "maxMemoryMB": 2048,
    "gcInterval": "30s"
  },
  "metrics": {
    "enabled": true,
    "logInterval": "10s"
  }
}
```

---

## 版本

- **API Version**: 1.0.0
- **Gateway Version**: stratix-gateway v1.0

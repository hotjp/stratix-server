/**
 * Stratix Gateway Electron Client SDK
 * 
 * 用法示例：
 * ```typescript
 * import { StratixClient } from './stratix-client';
 * 
 * const client = new StratixClient({
 *   clientId: 'client-123',
 *   gatewayUrl: 'wss://gateway.example.com/ws',
 *   autoReconnect: true
 * });
 * 
 * await client.connect();
 * await client.send('Hello World');
 * 
 * client.onMessage((msg) => {
 *   console.log('Received:', msg);
 * });
 * ```
 */

export interface StratixConfig {
  clientId: string;
  gatewayUrl: string;
  autoReconnect?: boolean;
  reconnectInterval?: number;
  maxReconnectAttempts?: number;
  heartbeatInterval?: number;
}

export interface StratixMessage {
  type: number;
  clientId: string;
  messageId: string;
  timestamp: number;
  data: string;
  route?: string;
}

export type MessageHandler = (msg: StratixMessage) => void;
export type ConnectionHandler = () => void;
export type ErrorHandler = (error: Error) => void;

export class StratixClient {
  private config: Required<StratixConfig>;
  private ws: WebSocket | null = null;
  private messageQueue: StratixMessage[] = [];
  private isConnected = false;
  private reconnectAttempts = 0;
  private heartbeatTimer: NodeJS.Timeout | null = null;
  private messageIdCounter = 0;

  private messageHandlers: MessageHandler[] = [];
  private connectHandlers: ConnectionHandler[] = [];
  private disconnectHandlers: ConnectionHandler[] = [];
  private errorHandlers: ErrorHandler[] = [];

  constructor(config: StratixConfig) {
    this.config = {
      autoReconnect: true,
      reconnectInterval: 3000,
      maxReconnectAttempts: 10,
      heartbeat: true,
      ...config
    };
  }

  /**
   * 连接到网关
   */
  async connect(): Promise<void> {
    if (this.ws?.readyState === WebSocket.OPEN) {
      return;
    }

    const url = `${this.config.gatewayUrl}?clientId=${this.config.clientId}`;
    
    return new Promise((resolve, reject) => {
      this.ws = new WebSocket(url);

      this.ws.onopen = () => {
        this.isConnected = true;
        this.reconnectAttempts = 0;
        this.startHeartbeat();
        
        // 发送队列中的消息
        this.flushMessageQueue();
        
        this.connectHandlers.forEach(h => h());
        resolve();
      };

      this.ws.onmessage = (event) => {
        try {
          const msg: StratixMessage = JSON.parse(event.data);
          this.messageHandlers.forEach(h => h(msg));
        } catch (error) {
          this.errorHandlers.forEach(h => h(error as Error));
        }
      };

      this.ws.onerror = (error) => {
        this.errorHandlers.forEach(h => h(new Error('WebSocket error')));
        reject(error);
      };

      this.ws.onclose = () => {
        this.isConnected = false;
        this.stopHeartbeat();
        
        this.disconnectHandlers.forEach(h => h());

        if (this.config.autoReconnect && 
            this.reconnectAttempts < this.config.maxReconnectAttempts) {
          setTimeout(() => {
            this.reconnectAttempts++;
            this.connect().catch(err => {
              console.error('Reconnect failed:', err);
            });
          }, this.config.reconnectInterval);
        }
      };
    });
  }

  /**
   * 断开连接
   */
  disconnect(): void {
    this.config.autoReconnect = false;
    this.ws?.close();
    this.isConnected = false;
    this.stopHeartbeat();
  }

  /**
   * 发送消息
   */
  async send(data: string | ArrayBuffer): Promise<void> {
    const message: StratixMessage = {
      type: typeof data === 'string' ? 0 : 1,
      clientId: this.config.clientId,
      messageId: this.generateMessageId(),
      timestamp: Date.now(),
      data: typeof data === 'string' ? data : this.arrayBufferToBase64(data)
    };

    if (this.isConnected && this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(message));
    } else {
      // 加入队列等待重连后发送
      this.messageQueue.push(message);
    }
  }

  /**
   * 注册消息处理器
   */
  onMessage(handler: MessageHandler): void {
    this.messageHandlers.push(handler);
  }

  /**
   * 注册连接成功处理器
   */
  onConnect(handler: ConnectionHandler): void {
    this.connectHandlers.push(handler);
  }

  /**
   * 注册断开连接处理器
   */
  onDisconnect(handler: ConnectionHandler): void {
    this.disconnectHandlers.push(handler);
  }

  /**
   * 注册错误处理器
   */
  onError(handler: ErrorHandler): void {
    this.errorHandlers.push(handler);
  }

  /**
   * 获取连接状态
   */
  getConnectionState(): boolean {
    return this.isConnected;
  }

  /**
   * 获取队列中的消息数量
   */
  getQueueSize(): number {
    return this.messageQueue.length;
  }

  private generateMessageId(): string {
    return `msg-${this.config.clientId}-${Date.now()}-${this.messageIdCounter++}`;
  }

  private arrayBufferToBase64(buffer: ArrayBuffer): string {
    const bytes = new Uint8Array(buffer);
    let binary = '';
    for (let i = 0; i < bytes.byteLength; i++) {
      binary += String.fromCharCode(bytes[i]);
    }
    return btoa(binary);
  }

  private startHeartbeat(): void {
    if (!this.config.heartbeat) {
      return;
    }
    
    this.heartbeatTimer = setInterval(() => {
      if (this.ws?.readyState === WebSocket.OPEN) {
        this.ws.send(JSON.stringify({
          type: 2, // Control message
          clientId: this.config.clientId,
          messageId: this.generateMessageId(),
          timestamp: Date.now(),
          data: 'ping'
        }));
      }
    }, this.config.heartbeatInterval);
  }

  private stopHeartbeat(): void {
    if (this.heartbeatTimer) {
      clearInterval(this.heartbeatTimer);
      this.heartbeatTimer = null;
    }
  }

  private flushMessageQueue(): void {
    while (this.messageQueue.length > 0 && 
           this.ws?.readyState === WebSocket.OPEN) {
      const message = this.messageQueue.shift();
      if (message) {
        this.ws.send(JSON.stringify(message));
      }
    }
  }
}

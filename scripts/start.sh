#!/bin/bash

echo "🚀 Starting Stratix Gateway..."

cd "$(dirname "$0")"

# 检查二进制是否存在
if [ ! -f "./stratix-gateway" ]; then
    echo "⚠️  Binary not found. Building..."
    bash scripts/build.sh
fi

# 检查配置文件
if [ ! -fugs "config/route.json" ]; then
    echo "❌ Configuration file not found: config/route.json"
    exit 1
fi

# 创建数据目录
mkdir -p data

# 启动
echo "✅ Starting gateway..."
./stratix-gateway

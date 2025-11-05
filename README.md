# Remote Command Executor

一个基于 Golang 的远程 PowerShell 命令执行服务。

## 功能

- 创建独立的 PowerShell 会话
- 在指定会话中执行命令
- 管理和关闭会话

## API 接口

### 1. 启动会话
**Endpoint:** `POST /start-session`

**Response:**
```json
{
  "session_id": "uuid-string"
}
```

### 2. 执行命令
**Endpoint:** `POST /run-command`

**Request Body:**
```json
{
  "sessionid": "uuid-string",
  "command": "echo 123xxx"
}
```

**Response:**
```json
{
  "output": "命令输出结果"
}
```

### 3. 结束会话
**Endpoint:** `POST /end-session`

**Request Body:**
```json
{
  "sessionid": "uuid-string"
}
```

**Response:**
```json
{
  "message": "Session ended successfully"
}
```

## 运行

```bash
go run main.go
```

服务将在 `http://localhost:8833` 启动。

## 测试示例

使用 PowerShell 测试：

```powershell
# 1. 启动会话
$response = Invoke-RestMethod -Uri "http://localhost:8833/start-session" -Method Post
$sessionId = $response.session_id

# 2. 执行命令
$body = @{
    sessionid = $sessionId
    command = "echo 'Hello World'"
} | ConvertTo-Json

Invoke-RestMethod -Uri "http://localhost:8833/run-command" -Method Post -Body $body -ContentType "application/json"

# 3. 结束会话
$body = @{
    sessionid = $sessionId
} | ConvertTo-Json

Invoke-RestMethod -Uri "http://localhost:8833/end-session" -Method Post -Body $body -ContentType "application/json"
```

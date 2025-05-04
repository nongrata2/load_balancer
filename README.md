# HTTP Load Balancer

## 📌 Overview
A high-performance HTTP load balancer implemented in Go that distributes incoming requests across multiple backend servers using configurable strategies.

## ✨ Key Features

### 🔄 Round-Robin Algorithm
- Distributes requests sequentially across all available backend servers
- Automatically skips unavailable servers during distribution
- Ensures equal request distribution among healthy backends

### ⚖️ Least Connections Algorithm
- Selects the backend server with the fewest active connections
- Provides better performance under long-lived or unevenly distributed workloads
- Tracks connection count in real-time per backend

### 🎲 Random Algorithm
- Chooses a random healthy backend for each request
- Uses a cryptographically secure random source for fair distribution
- Scales well with large numbers of backends

### 🩺 Health Monitoring System
- **Interval**: Active health checks every **10 seconds**
- **Check Method**: Sends HTTP GET requests to backend root URLs
- **Availability Criteria**:
  - Valid HTTP response (2xx/3xx status codes)
  - Response time < 5 seconds threshold
- **Self-healing**: Automatically reintegrates recovered backends into rotation


## Quick start
1. Clone the repository
```sh
git clone https://github.com/nongrata2/load_balancer
cd load_balancer
```
2. Create and fill config.yaml file. Spicify address for load balancer, address of all backend servers and algorthm(random, roundrobin or leastconnections). For example:
```
address: localhost:8080
backends:
  - "http://localhost:8081"
  - "http://localhost:8082"
  - "http://localhost:8083"
algorithm: "roundrobin"
```
3. Run the load balancer:
```sh
go run cmd/balancer/main.go
```

For testing you can use cmd/mock/main.go
Function starts mock server on specified port
```go
startMockServer(PORT, DELAY)
```
For example:
```go
startMockServer("8081", 3*time.Second)
```

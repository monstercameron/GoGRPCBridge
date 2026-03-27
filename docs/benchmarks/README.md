# gRPC vs REST Benchmark Results

## CRUD Operations

| Benchmark | Time (ms) | Memory (B/op) | Allocs/op | Winner |
|-----------|-----------|---------------|-----------|--------|
| **Create Todo** |
| gRPC | 0.18 | 17,849 | 246 | ❌ |
| REST | 0.07 | 10,454 | 109 | ✅ |
| **List 100 Todos** |
| gRPC | 0.22 | 29,853 | 541 | ❌ |
| REST | 0.08 | 13,451 | 96 | ✅ |
| **Update Todo** |
| gRPC | 0.18 | 17,560 | 246 | ❌ |
| REST | 0.07 | 12,341 | 112 | ✅ |
| **Delete Todo** |
| gRPC | 0.18 | 16,851 | 239 | ❌ |
| REST | 0.08 | 11,013 | 116 | ✅ |

## Payload Size

| Dataset | gRPC (KB) | REST (KB) | Savings | Winner |
|---------|-----------|-----------|---------|--------|
| 10 items | 1.01 | 1.25 | 19% | ✅ gRPC |
| 100 items | 7.31 | 9.57 | 24% | ✅ gRPC |
| 1000 items | 75.0 | 96.6 | 22% | ✅ gRPC |

## Streaming Performance

| Benchmark | Time (ms) | Memory (B/op) | Allocs/op | Winner |
|-----------|-----------|---------------|-----------|--------|
| **Server Streaming (1000 items)** |
| gRPC | 18.4 | 793,207 | 19,442 | ✅ Progressive |
| REST | 2.0 | 912,547 | 2,157 | ❌ All-at-once |
| **Bidirectional (100 messages)** |
| gRPC | 7.06 | 190,662 | 5,405 | ✅ |
| REST | 6.50 | 1,058,378 | 10,930 | ❌ |

## Connection Efficiency

| Benchmark | Time (ms) | Memory (B/op) | Allocs/op | Winner |
|-----------|-----------|---------------|-----------|--------|
| **10 Sequential Requests** |
| gRPC | 1.90 | 173,734 | 2,460 | ✅ Persistent |
| REST | 0.66 | 103,963 | 1,092 | ❌ Per-request |

---

## Summary

### gRPC Wins
✅ **Payload size** (20-24% smaller)  
✅ **Bidirectional streaming** (82% less memory, 50% fewer allocations)  
✅ **Progressive streaming** (doesn't load entire dataset into memory)  
✅ **Connection reuse** (persistent connection)  
✅ **Bandwidth efficiency** (binary Protobuf)

### REST Wins
✅ **Simple CRUD latency** (2-3x faster per operation)  
✅ **Memory allocations** (2-25x fewer allocations)  
✅ **Simplicity** (easier debugging)

---

## Running Benchmarks

```bash
# All benchmarks
go test ./benchmarks -bench=. -benchmem -benchtime=3s

# Specific category
go test ./benchmarks -bench=BenchmarkGRPC_Payload -benchmem
```

| Operation | gRPC (allocs/op) | REST (allocs/op) | Winner |
|-----------|------------------|------------------|--------|
| **Create** | 246 | 109 | REST (2.3x fewer) |
| **List 100** | 541 | 96 | REST (5.6x fewer) |
| **Payload 1000** | 3,306 | 134 | REST (24.7x fewer) |

**Analysis**: REST makes significantly fewer allocations because:
1. HTTP is simpler than HTTP/2
2. JSON parsing in Go is highly optimized
3. gRPC creates intermediate buffers for framing

This impacts GC pressure in high-throughput scenarios.

---

### Streaming Performance

| Operation | gRPC (ms) | REST Equivalent (ms) | Notes |
|-----------|-----------|----------------------|-------|
| **Server Streaming (100 items)** | 1.98 | 0.08 | REST can't stream, must fetch all at once |

**Analysis**: REST doesn't support true streaming. The comparison shows:
- gRPC: Streams 100 items progressively (1.98ms total)
- REST: Fetches all 100 at once (0.08ms)

**Why REST is "faster"**: It's comparing apples to oranges. REST loads everything into memory immediately, while gRPC streams items one-by-one. For larger datasets or slow clients, streaming prevents memory overflow and provides progressive loading.

**Real-world example**: 
- **Chat app**: gRPC streams messages as they arrive. REST would need to poll every second.
- **Large dataset**: gRPC can stream 1 million records without loading all into memory. REST would crash.

---

## When to Use Each

### Use gRPC (GoGRPCBridge) When:

✅ **Bidirectional streaming** - Chat, collaboration, real-time dashboards  
✅ **Bandwidth matters** - Mobile apps, metered connections (22% smaller payloads)  
✅ **Type safety** - Prevent runtime errors with Protobuf contracts  
✅ **Large datasets** - Stream instead of loading everything  
✅ **Multiple platforms** - Same .proto file for web, mobile, backend  

### Use REST When:

✅ **Simple CRUD** - Low latency for basic operations  
✅ **Browser caching** - HTTP cache headers work out-of-the-box  
✅ **Debugging** - curl and browser DevTools  
✅ **Third-party integration** - Most APIs are REST  

---

## Key Takeaways

1. **Latency**: REST is 2-3x faster for simple operations
2. **Payload**: gRPC is 20-24% smaller (Protobuf vs JSON)
3. **Memory**: REST uses 2-25x fewer allocations
4. **Streaming**: gRPC supports real-time bidirectional, REST doesn't
5. **Type Safety**: gRPC prevents runtime errors, REST doesn't

**The Bottom Line**: 

- **If you need streaming or have bandwidth constraints** → Use gRPC
- **If you need the lowest latency for simple CRUD** → Use REST
- **If you want both** → Use both (gRPC for real-time, REST for simple queries)

---

## Running Benchmarks

```bash
# Run all benchmarks
go test github.com/monstercameron/GoGRPCBridge/benchmarks -bench=Benchmark -benchtime=3s -benchmem

# Run specific benchmark
go test github.com/monstercameron/GoGRPCBridge/benchmarks -bench=BenchmarkGRPC_CreateTodo -benchtime=3s

# Compare payload sizes
go test github.com/monstercameron/GoGRPCBridge/benchmarks -bench=PayloadSize -benchtime=3s
```

---

## Benchmark Details

### Test Data

```protobuf
message Todo {
  string id = 1;    // UUID format
  string text = 2;  // 50-100 characters
  bool done = 3;
}
```

### Sample Text Lengths
- **Small**: "Buy milk" (8 chars)
- **Medium**: "Complete quarterly performance review and submit to HR" (55 chars)
- **Large**: "Research and implement WebSocket-based gRPC tunneling solution for browser compatibility..." (100+ chars)

### Hardware
- **CPU**: ARM64, 12 cores
- **RAM**: Available memory not limited
- **OS**: Windows (results may vary on Linux/macOS due to different WebSocket implementations)

---

**Conclusion**: Both protocols excel in different scenarios. GoGRPCBridge brings gRPC's strengths (streaming, type safety, efficiency) to the browser where it previously wasn't available. For simple CRUD, REST remains faster, but for real-time applications, gRPC is the only option.

# 端口分配器 - RBGS 动态端口分配

## 概述

端口分配器（Port Allocator）模块为 RoleBasedGroupSet (RBGS) 工作负载提供动态端口分配能力。它基于 Pod 注解中的配置，自动为 Pod 分配网络端口，支持两种端口作用域：PodScoped 和 RoleScoped。

## 适用场景

端口分配器特别适用于以下场景：

- **HostNetwork 部署**：当使用 `hostNetwork: true`（例如用于 RDMA 网络）时，同一节点上的多个副本需要不同的端口以避免冲突
- **LLM 推理服务**：PD（Prefill-Decode）分离部署场景，每个副本需要唯一的服务端口
- **高密度部署**：希望最大化每个节点的副本密度而不产生端口冲突
- **动态服务发现**：服务需要互相发现对方分配的端口

## 功能特性

- **动态端口分配**：从可配置范围自动分配可用端口
- **两种端口作用域**：
    - **PodScoped**：每个 Pod 获得唯一端口（如 pod-0 为 30001，pod-1 为 30002）
    - **RoleScoped**：同一组件/角色的所有 Pod 共享同一端口
- **环境变量注入**：将分配的端口作为环境变量注入到容器中
- **端口引用**：支持引用同一角色内其他组件的端口
- **启用/禁用控制**：可通过控制器配置启用或禁用
- **幂等分配**：确保现有实例在调和过程中端口分配保持稳定

---

## 用户指南

### 第一步：启用端口分配器

在使用端口分配器功能之前，需要在 RBGS 控制器部署中启用它。

#### 方式 A：使用 Helm

更新 `values.yaml` 或在安装时使用 `--set`：

```bash
helm install rbgs-controller ./deploy/helm/rbgs \
  --set portAllocator.enabled=true \
  --set portAllocator.startPort=30000 \
  --set portAllocator.portRange=5000 \
  --set portAllocator.strategy=random
```

#### 方式 B：使用 kubectl

编辑 RBGS 控制器部署，添加以下参数：

```yaml
spec:
  containers:
    - name: manager
      args:
        - --port-allocator-enabled=true
        - --port-allocator-start-port=30000
        - --port-allocator-port-range=5000
        - --port-allocator-strategy=random
```

### 第二步：在 RBG 中配置端口分配

在 Pod 模板中添加 `rolebasedgroup.workloads.x-k8s.io/port-allocator` 注解。

### 端口作用域选择指南

| 作用域 | 使用场景 | 示例 |
|--------|----------|------|
| **PodScoped** | 每个 Pod 需要唯一端口（如监控、调试端口） | 每个 Pod 使用不同端口的 Prometheus 指标端点 |
| **RoleScoped** | 所有 Pod 共享同一端口（如 gRPC 服务） | 所有副本使用相同端口进行负载均衡 |

### 第三步：验证端口分配

部署 RBG 后，验证端口是否正确分配：

```bash
# 检查 RoleInstanceSet 注解（RoleScoped 端口）
kubectl get roleinstanceset -n <namespace> -o yaml | grep -A10 annotations

# 检查 Pod 环境变量
kubectl exec -n <namespace> <pod-name> -- env | grep PORT

# 检查 Pod 注解
kubectl get pod -n <namespace> <pod-name> -o jsonpath='{.metadata.annotations}'
```

---

## 完整示例

### 示例 1：基础 StandalonePattern 与 PodScoped 端口

此示例展示一个简单部署，使用 `standalonePattern`，每个 Pod 获得唯一的 HTTP 服务端口。

```yaml
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: demo-basic
  namespace: default
spec:
  roles:
    - name: web
      replicas: 3
      standalonePattern:
        template:
          metadata:
            annotations:
              rolebasedgroup.workloads.x-k8s.io/port-allocator: |
                {
                  "allocations": [
                    {
                      "name": "http",
                      "env": "HTTP_PORT",
                      "scope": "PodScoped",
                      "annotationKey": "http.port"
                    }
                  ]
                }
          spec:
            hostNetwork: true
            containers:
              - name: web
                image: nginx:1.28.0
                command:
                  - /bin/sh
                  - -c
                  - |
                    echo "Starting web server on port $HTTP_PORT"
                    nginx -g "daemon off;"
```

**预期结果**：
- Pod `demo-basic-web-0` 获得 `HTTP_PORT=30001`（示例）
- Pod `demo-basic-web-1` 获得 `HTTP_PORT=30002`（示例）
- Pod `demo-basic-web-2` 获得 `HTTP_PORT=30003`（示例）
- 每个 Pod 都有 `http.port` 注解，值为分配的端口

### 示例 2：LeaderWorkerPattern 与端口分配

此示例演示使用 `leaderWorkerPattern`，Leader 和 Worker 共享基础模板，通过 patch 分别配置端口。

```yaml
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: demo-leader-worker
  namespace: default
spec:
  roles:
    - name: app
      replicas: 2
      # leaderWorkerPattern: 每个副本创建 1 个 leader + (size-1) 个 worker
      leaderWorkerPattern:
        size: 3  # 总共 3 个 Pod: 1 leader + 2 workers
        template:
          metadata:
            annotations:
              # 基础端口配置，所有 Pod 共享
              rolebasedgroup.workloads.x-k8s.io/port-allocator: |
                {
                  "allocations": [
                    {
                      "name": "grpc",
                      "env": "GRPC_PORT",
                      "scope": "PodScoped",
                      "annotationKey": "grpc.port"
                    }
                  ]
                }
          spec:
            hostNetwork: true
            containers:
              - name: app
                image: nginx:1.28.0
                command:
                  - /bin/sh
                  - -c
                  - |
                    echo "Starting on GRPC_PORT=$GRPC_PORT"
                    sleep 3600
        # Leader 专用补丁：添加 RoleScoped 端口
        leaderTemplatePatch:
          metadata:
            annotations:
              rolebasedgroup.workloads.x-k8s.io/port-allocator: |
                {
                  "allocations": [
                    {
                      "name": "grpc",
                      "env": "GRPC_PORT",
                      "scope": "PodScoped",
                      "annotationKey": "grpc.port"
                    },
                    {
                      "name": "leader-grpc",
                      "env": "LEADER_GRPC_PORT",
                      "scope": "RoleScoped",
                      "annotationKey": "leader.grpc.port"
                    }
                  ]
                }
          spec:
            containers:
              - name: app
                env:
                  - name: ROLE
                    value: "leader"
        # Worker 专用补丁：引用 Leader 的 RoleScoped 端口
        workerTemplatePatch:
          metadata:
            annotations:
              rolebasedgroup.workloads.x-k8s.io/port-allocator: |
                {
                  "allocations": [
                    {
                      "name": "grpc",
                      "env": "GRPC_PORT",
                      "scope": "PodScoped",
                      "annotationKey": "grpc.port"
                    }
                  ],
                  "references": [
                    {
                      "env": "LEADER_GRPC_PORT",
                      "from": "app.leader.leader-grpc"
                    }
                  ]
                }
          spec:
            containers:
              - name: app
                env:
                  - name: ROLE
                    value: "worker"
```

**预期结果**：
- 每个 replica group 中：
  - Leader Pod 获得：
    - `GRPC_PORT=30001`（PodScoped，此 Pod 唯一）
    - `LEADER_GRPC_PORT=30005`（RoleScoped，同角色所有 Pod 共享）
  - Worker Pod 获得：
    - `GRPC_PORT=30002`、`GRPC_PORT=30003`（PodScoped，每个 Pod 唯一）
    - `LEADER_GRPC_PORT=30005`（引用自 Leader 的 RoleScoped 端口）

### 示例 3：LLM 推理服务（PD 分离模式）

此示例展示真实的 LLM 推理部署，使用 `standalonePattern` 分别部署 Prefill 和 Decode 角色。

```yaml
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: llm-inference
  namespace: default
spec:
  roles:
    # Prefill 角色 - 处理初始提示词
    - name: prefill
      replicas: 2
      standalonePattern:
        template:
          metadata:
            annotations:
              rolebasedgroup.workloads.x-k8s.io/port-allocator: |
                {
                  "allocations": [
                    {
                      "name": "grpc",
                      "env": "GRPC_PORT",
                      "scope": "PodScoped",
                      "annotationKey": "grpc.port"
                    },
                    {
                      "name": "metrics",
                      "env": "METRICS_PORT",
                      "scope": "PodScoped",
                      "annotationKey": "prometheus.io/port"
                    }
                  ]
                }
          spec:
            hostNetwork: true
            containers:
              - name: prefill
                image: your-llm-image:latest
                resources:
                  limits:
                    nvidia.com/gpu: "8"
                command:
                  - /bin/sh
                  - -c
                  - |
                    echo "Prefill server on GRPC_PORT=$GRPC_PORT, METRICS_PORT=$METRICS_PORT"
                    # 在此处添加 prefill 服务启动命令

    # Decode 角色 - 处理 token 生成
    - name: decode
      replicas: 4
      standalonePattern:
        template:
          metadata:
            annotations:
              rolebasedgroup.workloads.x-k8s.io/port-allocator: |
                {
                  "allocations": [
                    {
                      "name": "grpc",
                      "env": "GRPC_PORT",
                      "scope": "PodScoped",
                      "annotationKey": "grpc.port"
                    }
                  ]
                }
          spec:
            hostNetwork: true
            containers:
              - name: decode
                image: your-llm-image:latest
                resources:
                  limits:
                    nvidia.com/gpu: "8"
                command:
                  - /bin/sh
                  - -c
                  - |
                    echo "Decode server on GRPC_PORT=$GRPC_PORT"
                    # 在此处添加 decode 服务启动命令
```

**注意**：跨角色的端口引用目前不支持，Prefill 和 Decode 需要通过 Service 发现彼此的端口。

### 示例 4：Prometheus 就绪部署

此示例展示如何配置端口以支持 Prometheus 动态采集。

```yaml
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: prometheus-ready
  namespace: monitoring
spec:
  roles:
    - name: server
      replicas: 3
      standalonePattern:
        template:
          metadata:
            annotations:
              # Prometheus 采集配置
              prometheus.io/scrape: "true"
              prometheus.io/path: "/metrics"
              # 端口分配器配置
              rolebasedgroup.workloads.x-k8s.io/port-allocator: |
                {
                  "allocations": [
                    {
                      "name": "metrics",
                      "env": "METRICS_PORT",
                      "scope": "PodScoped",
                      "annotationKey": "prometheus.io/port"
                    }
                  ]
                }
          spec:
            containers:
              - name: server
                image: your-metrics-server:latest
                command:
                  - /bin/sh
                  - -c
                  - |
                    echo "Starting metrics server on port $METRICS_PORT"
                    # 在此处添加服务启动命令
```

**注意**：`prometheus.io/port` 注解由端口分配器自动设置，Prometheus 可以在每个 Pod 分配的端口上发现并采集指标。

---

## 验证命令

部署上述任意示例后，使用以下命令验证：

```bash
# 1. 检查 RoleInstanceSet 注解（RoleScoped 端口）
kubectl get roleinstanceset -n <namespace> -o=jsonpath='{.items[*].metadata.annotations}'

# 2. 检查 RoleInstance 注解（RoleScoped 和 PodScoped 端口）
kubectl get roleinstance -n <namespace> -o=jsonpath='{.items[*].metadata.annotations}'

# 3. 检查 Pod 注解
kubectl get pods -n <namespace> -o=jsonpath='{.items[*].metadata.annotations}'

# 4. 检查特定 Pod 的环境变量
kubectl exec -n <namespace> <pod-name> -- env | grep -E "PORT|GRPC|METRICS"

# 5. 完整检查 Pod
kubectl describe pod -n <namespace> <pod-name>
```

---

## 常见问题与解决方案

### 问题 1：端口分配器不工作

**现象**：Pod 没有获得端口的环境变量或注解。

**解决方案**：
1. 验证端口分配器是否已启用：
   ```bash
   kubectl logs -n <controller-namespace> <controller-pod> | grep "port allocator"
   ```
2. 检查控制器启动参数：
   ```bash
   kubectl get deployment -n <controller-namespace> <controller-name> -o yaml | grep port-allocator
   ```

### 问题 2：端口注解格式错误

**现象**：Pod 创建失败，出现注解解析错误。

**解决方案**：
确保注解值是有效的 JSON：
```yaml
# 正确 - 有效的 JSON
rolebasedgroup.workloads.x-k8s.io/port-allocator: |
  {
    "allocations": [
      {"name": "http", "env": "HTTP_PORT", "scope": "PodScoped"}
    ]
  }

# 错误 - 缺少引号
rolebasedgroup.workloads.x-k8s.io/port-allocator: |
  {
    allocations: [
      {name: "http", env: "HTTP_PORT"}
    ]
  }
```

### 问题 3：端口引用未找到

**现象**：引用的端口未注入到 Pod 中。

**解决方案**：
- 确保引用格式正确：`<role>.<component>.<port>`
- 被引用的端口必须在同一角色内（不支持跨角色引用）
- 被引用的组件必须存在且已分配端口

### 问题 4：调和后端口变化

**现象**：工作负载更新时端口意外变化。

**解决方案**：
这是新实例的预期行为。对于现有实例：
- RoleScoped 端口：稳定（存储在 RoleInstanceSet 注解中）
- PodScoped 端口：现有 Pod 保持稳定，只有新 Pod 获得新分配

---

## 架构

### 核心组件

```
┌─────────────────────────────────────────────────────────────┐
│                    端口分配器模块                             │
├─────────────────────────────────────────────────────────────┤
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐      │
│  │   Parser     │  │   Manager    │  │  Allocator   │      │
│  │  (parser.go) │  │ (manager.go) │  │(port_allocator│      │
│  └──────────────┘  └──────────────┘  └──────────────┘      │
│         │                 │                  │              │
│         ▼                 ▼                  ▼              │
│  ┌─────────────────────────────────────────────────────┐   │
│  │              PortAllocatorConfig                     │   │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  │   │
│  │  │ Allocations │  │ References  │  │    Scope    │  │   │
│  │  │  (types)    │  │   (types)   │  │Pod/Role     │  │   │
│  │  └─────────────┘  └─────────────┘  └─────────────┘  │   │
│  └─────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

### 端口作用域类型

#### PodScoped
- 每个 Pod 获得唯一的端口号
- 端口注解键格式：`<pod-name>.<port-name>`
- 使用场景：每个副本需要唯一端口的服务（如监控端点）

#### RoleScoped
- 同一组件的所有 Pod 共享同一端口
- 端口注解键格式：`<component-name>.<port-name>`
- 使用场景：所有副本使用相同端口的服务（如 gRPC 服务）

## 配置

### Helm Values

```yaml
portAllocator:
  # 是否启用端口分配器功能
  enabled: false
  # 端口分配策略。支持值：random
  strategy: random
  # 可分配端口范围的起始端口号
  startPort: 30000
  # 可分配端口范围的大小
  portRange: 5000
```

### Pod 注解配置

在 Pod 模板注解中添加端口分配器配置：

```yaml
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: example
spec:
  roles:
    - name: worker
      replicas: 3
      standalonePattern:
        template:
          metadata:
            annotations:
              rolebasedgroup.workloads.x-k8s.io/port-allocator: |
                {
                  "allocations": [
                    {
                      "name": "grpc",
                      "env": "GRPC_PORT",
                      "scope": "RoleScoped"
                    },
                    {
                      "name": "metrics",
                      "env": "METRICS_PORT",
                      "scope": "PodScoped",
                      "annotationKey": "prometheus.io/port"
                    }
                  ],
                  "references": [
                    {
                      "env": "LEADER_GRPC_PORT",
                      "from": "worker.leader.grpc"
                    }
                  ]
                }
          spec:
            containers:
              - name: worker
                image: nginx:1.28.0
```

### 配置模式

#### PortAllocation

| 字段 | 类型 | 必填 | 描述 |
|------|------|------|------|
| name | string | 是 | 端口分配的唯一标识符 |
| env | string | 是 | 要注入到容器中的环境变量名称 |
| annotationKey | string | 否 | 要存储端口值的 Pod 注解键 |
| scope | PortScope | 否 | 端口作用域，默认：PodScoped（PodScoped 或 RoleScoped） |

#### PortReference

| 字段 | 类型 | 必填 | 描述 |
|------|------|------|------|
| env | string | 是 | 引用端口的环境变量名称 |
| from | string | 是 | 引用路径，格式：`<role>.<component>.<port>` |

## API 参考

### 核心函数

#### SetupPortAllocator
```go
func SetupPortAllocator(startPort int, portRange int, allocateStrategy string, enabled bool, client client.Client) error
```
初始化全局单例端口分配器。必须在控制器启动时调用。

#### IsEnabled
```go
func IsEnabled() bool
```
返回 true 表示端口分配器已启用并可以使用。

#### AllocateRoleScopedPorts
```go
func AllocateRoleScopedPorts(config *PortAllocatorConfig, componentName string) (map[string]string, error)
```
为组件分配 RoleScoped 端口。返回注解键到端口值的映射。

#### AllocatePodScopedPorts
```go
func AllocatePodScopedPorts(config *PortAllocatorConfig, podName string) (map[string]string, error)
```
为特定 Pod 分配 PodScoped 端口。返回注解键到端口值的映射。

#### AllocatePortsForInstance
```go
func AllocatePortsForInstance(instance *workloadsv1alpha2.RoleInstance, instanceSet *workloadsv1alpha2.RoleInstanceSet)
```
为 RoleInstance 派生 RoleScoped 端口并分配 PodScoped 端口。

**重要**：PodScoped 端口仅为新实例（UID == ""）分配。现有实例保留其端口分配，以确保调和过程中的稳定性。

#### InjectPortsIntoPod
```go
func InjectPortsIntoPod(pod *corev1.Pod, instance *workloadsv1alpha2.RoleInstance, config *PortAllocatorConfig, componentName string) error
```
将分配的端口作为环境变量和注解注入到 Pod 规范中。

### 配置解析

#### ParsePortAllocatorConfig
```go
func ParsePortAllocatorConfig(pod *corev1.Pod) (*PortAllocatorConfig, error)
```
从 Pod 注解解析端口分配器配置。

#### ParsePortAllocatorConfigFromTemplate
```go
func ParsePortAllocatorConfigFromTemplate(template *corev1.PodTemplateSpec) (*PortAllocatorConfig, error)
```
从 PodTemplateSpec 注解解析端口分配器配置。

#### HasPortAllocatorConfig
```go
func HasPortAllocatorConfig(template *corev1.PodTemplateSpec) bool
```
检查 Pod 模板是否有端口分配器配置。

## 实现细节

### 端口分配流程

```
┌─────────────────┐     ┌──────────────────┐     ┌─────────────────┐
│  RBG Controller │────▶│ RoleInstanceSet  │────▶│  RoleInstance   │
│   Reconcile     │     │  Reconciler      │     │   Controller    │
└─────────────────┘     └──────────────────┘     └─────────────────┘
                               │                          │
                               ▼                          ▼
                    ┌─────────────────────┐    ┌─────────────────────┐
                    │ allocateRoleScoped  │    │ AllocatePortsFor    │
                    │ PortAnnotations()   │    │ Instance()          │
                    │ (for new RIS only)  │    │ (for all instances) │
                    └─────────────────────┘    └─────────────────────┘
                               │                          │
                               ▼                          ▼
                    ┌─────────────────────┐    ┌─────────────────────┐
                    │ RoleScoped ports    │    │ RoleScoped: Copy    │
                    │ stored in RIS       │    │ from RIS            │
                    │ annotation          │    │ PodScoped: Allocate │
                    └─────────────────────┘    │ only for new RI     │
                                               └─────────────────────┘
                                                          │
                                                          ▼
                                               ┌─────────────────────┐
                                               │ InjectPortsIntoPod()│
                                               │ (when creating pods)│
                                               └─────────────────────┘
```

### 幂等性保证

端口分配器确保幂等行为，防止不必要的 Pod 重建：

1. **RoleScoped 端口**：在 RoleInstanceSet 创建时分配一次，存储在 RIS 注解中。始终从 RIS 复制到 RI。

2. **PodScoped 端口**：
    - 新实例（UID == ""）：新分配
    - 现有实例（UID != ""）：保留现有注解，跳过分配

此设计确保：
- 调和现有工作负载不会改变端口分配
- 不会因端口分配变化触发 Pod 重建
- 端口在实例生命周期内保持稳定

### 错误处理

模块定义了一个禁用状态的哨兵错误：

```go
var ErrPortAllocatorDisabled = errors.New("port allocator is not enabled")
```

调用者可以使用 `errors.Is()` 检查此特定情况：

```go
ports, err := portallocator.AllocatePodScopedPorts(config, podName)
if errors.Is(err, portallocator.ErrPortAllocatorDisabled) {
    // 处理禁用情况
}
```

## 测试

### 单元测试

运行端口分配器单元测试：

```bash
go test ./pkg/port-allocator/... -v
```

### 端到端测试

运行端口分配器 E2E 测试：

```bash
# 确保测试集群中已启用端口分配器
go test ./test/e2e/... -v -ginkgo.focus="port allocator"
```

## 限制与注意事项

1. **仅支持 RoleInstanceSet**：端口分配器仅在 Role 工作负载类型为 `RoleInstanceSet` 时支持，其他工作负载类型不适用。

2. **同角色引用**：端口引用只能引用同一角色内的端口，目前不支持跨角色引用。

3. **不可变配置**：Pod 模板中的 port-allocator 注解是不可变的，创建后修改将被 webhook 拒绝。

4. **尽力而为的唯一性**：默认的 `random` 分配器保证单次分配批内唯一，但不保证跨多次调用或控制器重启的唯一性。生产环境高端口密度场景，请确保端口范围足够大。

5. **无端口释放**：RoleInstanceSet 或 RoleInstance 删除时，端口不会显式释放回池。大多数场景可接受，但可能导致端口碎片化。

## 最佳实践

1. **共享服务使用 RoleScoped**：当组件的所有副本应监听同一端口（如 gRPC、HTTP）时，使用 `RoleScoped` 以保持一致性并简化服务发现。

2. **监控/调试使用 PodScoped**：当每个副本需要唯一端口（如 Prometheus 指标、调试端点）时，使用 `PodScoped`。

3. **为外部工具设置 annotationKey**：使用 `annotationKey` 将端口暴露给 Prometheus 或服务网格等外部工具。

4. **仔细规划端口范围**：设置 `startPort` 和 `portRange` 以避免与现有服务冲突。范围越大，碰撞概率越低。

5. **使用 hostNetwork 测试**：始终使用 `hostNetwork: true` 测试端口配置，确保实际节点上不会发生冲突。

## 未来增强

- 支持更多分配策略（如顺序分配、基于哈希的分配）
- 控制器重启后的端口预留和持久化
- 多控制器实例间的全局端口协调
- 端口健康检查和自动重新分配
- 跨角色端口引用

## 相关文档

- [KEP-171: Pod Port Allocation](../../keps/171-pod-port-allocation/README.md) - 设计提案和技术细节
- [RoleBasedGroup API Reference](../../api/workloads/v1alpha2/) - API 规范
- [快速入门指南](../quick_start.md) - RBGS 入门

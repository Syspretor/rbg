# v1alpha1 到 v1alpha2 迁移指南

本文档介绍如何将 RoleBasedGroup (RBG) 从 `v1alpha1` API 版本迁移到 `v1alpha2`。

## 版本背景

- **v0.7.0-alpha1 / v0.7.0-alpha2**: v1alpha1 API 不可用
- **v0.7.0-alpha3 及 v0.7.0 正式版**: v1alpha2 作为 storage version，通过 conversion webhook 实现 v1alpha1 到 v1alpha2 的自动转换
- **v0.8.0**: 将移除兼容性转换逻辑

---

## 1. 无需手动干预的转换

在 v0.7.0 版本中，conversion webhook 会自动处理以下转换：

### 1.1 Workload 默认值转换

| v1alpha1 | v1alpha2 |
|----------|----------|
| 默认 `apps/v1/StatefulSet` | 默认 `workloads.x-k8s.io/v1alpha2/RoleInstanceSet` |

**转换行为**：当 v1alpha1 的 Role 未显式指定 workload 时，转换后会显式添加 `workload: {apiVersion: apps/v1, kind: StatefulSet}`。

> ⚠️ **重要提示**: Workload 字段已进入 Deprecated 流程，在 v0.8.0 版本中将放弃这个默认值的转换逻辑。建议尽早迁移到 RoleInstanceSet。

### 1.2 Coordination 字段转换

v1alpha1 中的 `spec.coordination` 字段会被转换到 v1alpha2 RBG 对象的 annotation 中，由 RBG controller 自动创建对应的 `CoordinatedPolicy` CR。

**转换逻辑**（仅在 v0.7.0 中存在，v0.8.0 将移除）：
```yaml
# v1alpha1
spec:
  coordination:
    - name: pd-rollout
      roles: [prefill, decode]
      strategy:
        rollingUpdate:
          maxSkew: "1%"
```

转换后，RBG annotation 会包含协调策略信息，controller 会据此创建 CoordinatedPolicy。

### 1.3 PodGroupPolicy 转换

v1alpha1 中的 `spec.podGroupPolicy` 字段会被转换到 v1alpha2 的 annotation 配置：

| v1alpha1 spec.podGroupPolicy | v1alpha2 annotation |
|------------------------------|---------------------|
| `kubeScheduling.scheduleTimeoutSeconds: 120` | `rbg.workloads.x-k8s.io/group-gang-scheduling: "true"`<br>`rbg.workloads.x-k8s.io/group-gang-scheduling-timeout: "120"` |
| `volcanoScheduling.queue: default` | `rbg.workloads.x-k8s.io/group-gang-scheduling: "true"`<br>`rbg.workloads.x-k8s.io/group-gang-scheduling-volcano-queue: "default"` |
| `volcanoScheduling.priorityClassName: high` | `rbg.workloads.x-k8s.io/group-gang-scheduling: "true"`<br>`rbg.workloads.x-k8s.io/group-gang-scheduling-volcano-priority: "high"` |

---

## 2. 需要手动修改的部分

### 2.1 手动创建 CoordinatedPolicy（推荐）

虽然 conversion webhook 会自动转换 coordination 字段，但我们**强烈建议**手动创建独立的 `CoordinatedPolicy` CR，以便更好地管理和维护协调策略。

**v1alpha1 示例**:
```yaml
apiVersion: workloads.x-k8s.io/v1alpha1
kind: RoleBasedGroup
metadata:
  name: nginx-cluster
spec:
  coordination:
    - name: pd-rollout
      roles:
        - prefill
        - decode
      strategy:
        rollingUpdate:
          maxSkew: "1%"
          maxUnavailable: "10%"
  roles:
    - name: prefill
      replicas: 7
      template:
        spec:
          containers:
            - name: nginx
              image: nginx:1.14
    - name: decode
      replicas: 3
      template:
        spec:
          containers:
            - name: nginx
              image: nginx:1.14
```

**v1alpha2 迁移后**（手动创建 CoordinatedPolicy）:
```yaml
# RoleBasedGroup
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: nginx-cluster
spec:
  roles:
    - name: prefill
      replicas: 7
      standalonePattern:
        template:
          spec:
            containers:
              - name: nginx
                image: nginx:1.14
    - name: decode
      replicas: 3
      standalonePattern:
        template:
          spec:
            containers:
              - name: nginx
                image: nginx:1.14

---
# CoordinatedPolicy（独立 CR）
apiVersion: workloads.x-k8s.io/v1alpha2
kind: CoordinatedPolicy
metadata:
  name: pd-rollout-policy
spec:
  policies:
    - name: pd-rollout
      roles:
        - prefill
        - decode
      strategy:
        rollingUpdate:
          maxSkew: "1%"
          maxUnavailable: "10%"
```

**CoordinatedPolicy Scaling 示例**:
```yaml
apiVersion: workloads.x-k8s.io/v1alpha2
kind: CoordinatedPolicy
metadata:
  name: scaling-policy
spec:
  policies:
    - name: coordinated-scaling
      roles:
        - prefill
        - decode
      strategy:
        scaling:
          maxSkew: "10%"
          progression: OrderScheduled  # 或 OrderReady
```

### 2.2 Helm 部署配置 Scheduler Name

当使用 gang scheduling 时，需要通过 Helm 参数配置 scheduler name，配合 PodGroup 相关的 annotation 使用。

**values.yaml 配置**:
```yaml
# 支持: scheduler-plugins（默认）, volcano
schedulerName: scheduler-plugins
```

**使用 Volcano 时**:
```yaml
schedulerName: volcano
```

**v1alpha2 RBG 示例（scheduler-plugins gang scheduling）**:
```yaml
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: scheduler-plugins-gang
  annotations:
    rbg.workloads.x-k8s.io/group-gang-scheduling: "true"
    rbg.workloads.x-k8s.io/group-gang-scheduling-timeout: "120"  # 可选，默认 60s
spec:
  roles:
    - name: prefill
      replicas: 2
      standalonePattern:
        template:
          spec:
            containers:
              - name: prefill
                image: nginx:1.14
                resources:
                  limits:
                    nvidia.com/gpu: "1"

    - name: decode
      replicas: 4
      standalonePattern:
        template:
          spec:
            containers:
              - name: decode
                image: nginx:1.14
                resources:
                  limits:
                    nvidia.com/gpu: "1"
```

**v1alpha2 RBG 示例（Volcano gang scheduling）**:
```yaml
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: volcano-gang
  annotations:
    rbg.workloads.x-k8s.io/group-gang-scheduling: "true"
    rbg.workloads.x-k8s.io/group-gang-scheduling-volcano-queue: "default"
    rbg.workloads.x-k8s.io/group-gang-scheduling-volcano-priority: "high-priority"
spec:
  roles:
    - name: prefill
      replicas: 2
      standalonePattern:
        template:
          spec:
            schedulerName: volcano  # 注意：使用 Volcano 时需要指定
            containers:
              - name: prefill
                image: nginx:1.14
                resources:
                  limits:
                    nvidia.com/gpu: "1"
```

### 2.3 环境变量迁移

此前依赖 LWS (LeaderWorkerSet) 时，业务需要适配 LWS 的环境变量。现在 RBG 使用自有的环境变量，不再依赖 LWS。

**环境变量对照表**:

| v1alpha1 (LWS 环境变量) | v1alpha2 (RBG 环境变量) | 说明 |
|------------------------|------------------------|------|
| `LWS_LEADER_ADDRESS` | `RBG_LWP_LEADER_ADDRESS` | Leader 组件的 DNS 地址 |
| `LWS_WORKER_INDEX` | `RBG_LWP_WORKER_INDEX` | 组件在 Instance 中的索引 |
| `LWS_GROUP_SIZE` | `RBG_LWP_GROUP_SIZE` | Instance 中组件总数 |

**其他 RBG 环境变量**:

| 环境变量 | 说明 |
|----------|------|
| `RBG_GROUP_NAME` | RBG 名称 |
| `RBG_ROLE_NAME` | Role 名称 |
| `RBG_ROLE_INDEX` | Pod 在 Role 中的序号 |
| `RBG_ROLE_INSTANCE_NAME` | RoleInstance 名称 |
| `RBG_COMPONENT_NAME` | 组件名称 |
| `RBG_COMPONENT_INDEX` | 组件在 Instance 中的索引 |

**业务代码适配示例**:

```python
# v1alpha1 - 使用 LWS 环境变量
import os
leader_address = os.environ.get("LWS_LEADER_ADDRESS")
worker_index = int(os.environ.get("LWS_WORKER_INDEX", "0"))
group_size = int(os.environ.get("LWS_GROUP_SIZE", "1"))

# v1alpha2 - 使用 RBG 环境变量
import os
leader_address = os.environ.get("RBG_LWP_LEADER_ADDRESS")
worker_index = int(os.environ.get("RBG_LWP_WORKER_INDEX", "0"))
group_size = int(os.environ.get("RBG_LWP_GROUP_SIZE", "1"))
```

> 💡 **提示**: 如果需要在 conversion webhook 中自动替换环境变量名称，请联系开发团队。

---

## 3. RoleSpec 结构变化

### 3.1 Template 配置方式

v1alpha2 引入了更清晰的 Pattern 结构：

**v1alpha1**:
```yaml
roles:
  - name: prefill
    replicas: 3
    template:  # 直接内联 template
      spec:
        containers:
          - name: nginx
            image: nginx:1.14
```

**v1alpha2**:
```yaml
roles:
  - name: prefill
    replicas: 3
    standalonePattern:  # 使用 standalonePattern 包装
      template:
        spec:
          containers:
            - name: nginx
            image: nginx:1.14
```

### 3.2 LeaderWorkerSet 配置

**v1alpha1**:
```yaml
roles:
  - name: lws-role
    replicas: 4
    workload:
      apiVersion: leaderworkerset.x-k8s.io/v1
      kind: LeaderWorkerSet
    leaderWorkerSet:
      size: 2
      patchLeaderTemplate: {...}
      patchWorkerTemplate: {...}
    template:
      spec:
        containers:
          - name: nginx
            image: nginx:1.14
```

**v1alpha2**:
```yaml
roles:
  - name: lws-role
    replicas: 4
    leaderWorkerPattern:
      size: 2
      leaderTemplatePatch: {...}
      workerTemplatePatch: {...}
      template:
        spec:
          containers:
            - name: nginx
              image: nginx:1.14
```

### 3.3 TemplateRef 使用

**v1alpha1**:
```yaml
roleTemplates:
  - name: base-template
    template:
      spec:
        containers:
          - name: nginx
            image: nginx:1.14

roles:
  - name: role-a
    templateRef:
      name: base-template
    templatePatch:  # patch 是单独字段
      spec:
        containers:
          - name: nginx
            resources:
              limits:
                memory: "512Mi"
```

**v1alpha2**:
```yaml
roleTemplates:
  - name: base-template
    template:
      spec:
        containers:
          - name: nginx
            image: nginx:1.14

roles:
  - name: role-a
    standalonePattern:
      templateRef:
        name: base-template
        patch:  # patch 合并到 templateRef 内部
          spec:
            containers:
              - name: nginx
                resources:
                  limits:
                    memory: "512Mi"
```

---

## 4. 迁移检查清单

- [ ] 确认 RBG controller 版本为 v0.7.0 或更高
- [ ] 检查 Workload 字段使用情况，考虑迁移到 RoleInstanceSet
- [ ] 将 `spec.coordination` 迁移到独立的 `CoordinatedPolicy` CR
- [ ] 将 `spec.podGroupPolicy` 迁移到 annotation 配置
- [ ] 更新业务代码中的环境变量名称（LWS_* → RBG_LWP_*）
- [ ] 通过 Helm 配置 `schedulerName` 参数
- [ ] 更新 RoleSpec 结构（standalonePattern / leaderWorkerPattern）
- [ ] 测试验证迁移后的功能正常

---

## 5. 完整迁移示例

### v1alpha1 原始配置

```yaml
apiVersion: workloads.x-k8s.io/v1alpha1
kind: RoleBasedGroup
metadata:
  name: inference-cluster
spec:
  podGroupPolicy:
    kubeScheduling:
      scheduleTimeoutSeconds: 120
  coordination:
    - name: pd-coordination
      roles:
        - prefill
        - decode
      strategy:
        rollingUpdate:
          maxSkew: "1%"
        scaling:
          maxSkew: "10%"
          progression: OrderScheduled
  roles:
    - name: prefill
      replicas: 7
      workload:
        apiVersion: apps/v1
        kind: StatefulSet
      template:
        spec:
          containers:
            - name: prefill
              image: inference:v1
              env:
                - name: LWS_LEADER_ADDRESS
                  valueFrom: ...
    - name: decode
      replicas: 3
      dependencies:
        - prefill
      template:
        spec:
          containers:
            - name: decode
              image: inference:v1
```

### v1alpha2 迁移后配置

```yaml
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: inference-cluster
  annotations:
    rbg.workloads.x-k8s.io/group-gang-scheduling: "true"
    rbg.workloads.x-k8s.io/group-gang-scheduling-timeout: "120"
spec:
  roles:
    - name: prefill
      replicas: 7
      workload:  # Deprecated，建议移除
        apiVersion: apps/v1
        kind: StatefulSet
      standalonePattern:
        template:
          spec:
            containers:
              - name: prefill
                image: inference:v1
                env:
                  - name: RBG_LWP_LEADER_ADDRESS  # 更新环境变量名
                    valueFrom: ...
    - name: decode
      replicas: 3
      dependencies:
        - prefill
      standalonePattern:
        template:
          spec:
            containers:
              - name: decode
                image: inference:v1

---
apiVersion: workloads.x-k8s.io/v1alpha2
kind: CoordinatedPolicy
metadata:
  name: pd-coordination-policy
spec:
  policies:
    - name: pd-coordination
      roles:
        - prefill
        - decode
      strategy:
        rollingUpdate:
          maxSkew: "1%"
        scaling:
          maxSkew: "10%"
          progression: OrderScheduled
```

---

## 6. 参考文档

- [CoordinatedPolicy API 示例](../examples/basic/coordinated-policy/)
- [Gang Scheduling 配置示例](../examples/basic/rbg/scheduling/)
- [Helm 部署配置](../deploy/helm/rbgs/values.yaml)

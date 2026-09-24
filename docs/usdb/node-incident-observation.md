# 节点严重事故观测

Chain runtime 的深重组保护在检测到 `upstream_reorg_epoch` 相对基线前进或回退时，
仍按原规则写入 `recovery/deep-btc-reorg/halted.json` 并以状态码 42 触发持久停机。
普通监控依赖错误仍使用状态码 43，保持原有恢复行为；本次变更不修改共识验证或自动恢复策略。

`usdb-deep-btc-reorg-incident:v1` 文件新增以下兼容字段：

| 字段 | 值与含义 |
| --- | --- |
| `incident_id` | 写入事故时生成的 UUID，32 位小写十六进制；重启不重新生成 |
| `code` | `DEEP_REORG_HALTED` |
| `severity` | `critical` |
| `recovery` | `manual_intervention` |

`reason`、`detected_at`、`baseline_epoch`、`observed_epoch` 等原字段保持不变。
文件原子替换后 fsync 父目录，保证事故记录的持久化边界包含目录更新。
旧 v1 事故没有新字段时仍阻止启动，不通过启动过程自动迁移或重写事故。

USDB node kit 将事故映射到 `observations.incidents`，供 `status --json`、
`status --progress-json`、watch 和私有控制台消费。映射只允许固定代码、时间、epoch
和事件 ID，不直接输出文件内的 `indexer_rpc_url`、完整 readiness 或其他任意诊断内容。
旧事故使用节点范围内的文件摘要标识；损坏的停机标记仍是停机证据，不视为恢复。

完整宿主机契约和运维边界见配套 usdb 仓库的
`doc/handbook/node/observations.md`。通知服务应使用节点身份、网络身份和事件 ID 去重；
邮件/webhook、持续告警窗口、事故确认和外部心跳不在本批范围内。

验证入口：

```bash
PYTHONDONTWRITEBYTECODE=1 python3 scripts/usdb/test_usdb_deep_reorg_guard.py
PYTHONDONTWRITEBYTECODE=1 python3 tests/test_runtime_deep_reorg.py
```

runtime 测试使用临时目录、回环 RPC 和 geth 替身，验证保护行为及重启，不替代线上 Docker 验收。

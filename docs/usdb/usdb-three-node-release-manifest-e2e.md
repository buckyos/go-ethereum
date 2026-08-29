# USDB 三节点 Release Manifest E2E

本文描述由跨仓 release candidate manifest 驱动的三节点真实服务链测试。测试目标不是从工作区直接启动
二进制，而是验证 release manifest 中冻结的三类 OCI 镜像能够组合为一致的网络：

- `usdb-bitcoin-core`：Bitcoin Core 28.1；
- `usdb-services`：balance-history、usdb-indexer 和 control-plane；
- `usdb-chain`：三个独立 USDB chain 节点。

测试只部署一套 Bitcoin、balance-history 和 indexer。三个 USDB chain 节点共同查询同一个冻结 external
state，但分别使用独立 datadir。这符合 validator 的确定性输入模型，也避免复制三份主网 UTXO 和 pass 索引。

## 两种运行模式

### CI release candidate

CI 模式直接使用 GitHub release candidate workflow 产生的 manifest。三个镜像均从 manifest 中的
`ghcr.io/buckyos/...@sha256:...` 拉取，不能回退到 tag、`latest` 或本地 image ID。

该模式要求：

1. 当前 checkout 与 manifest 中的三仓 revision、`scripts/usdb/ci-revisions.json` 一致；
2. testnet bundle 与 manifest 的 network identity 一致；
3. 节点私有 `node.env` 已配置 Bitcoin 数据目录、rpcauth 和 RPC credential；
4. Bitcoin 数据目录没有被另一个 `bitcoind` 进程占用。

### Local candidate

本地模式用于在 CI 发布前验证同一套 harness。构建器执行以下约束：

1. 用 `git archive HEAD` 导出各仓已提交内容，工作区未提交文件不会进入镜像；
2. 三个镜像推送到临时本地 OCI registry；
3. 使用 registry 返回的 OCI manifest digest 创建 production-shaped candidate manifest；
4. manifest 继续保存 canonical GHCR reference；`execution-plan.json` 只重写 registry transport，digest 不变；
5. 本地产物没有 GitHub provenance、attestation 或 Environment approval，不能发布或提升为正式 candidate。

临时 registry 默认使用 `registry:2`，仅属于测试基础设施，不属于 USDB release artifact。

## 本地候选构建

三个仓库默认位于同一个 workspace root：

```text
/home/bucky/work/go-ethereum
/home/bucky/work/usdb
/home/bucky/work/SourceDAO
```

使用新的输出目录执行构建；已有 candidate 不会被覆盖：

```bash
cd /home/bucky/work/go-ethereum

USDB_E2E_RELEASE_ID=usdb-testnet-v0-r999999 \
USDB_E2E_LOCAL_IMAGE_WORK_DIR=/tmp/usdb-three-node-local-r1 \
scripts/usdb/prepare_local_release_images.sh build
```

输出包括：

- `release-manifest.json`；
- `local-ci-revisions.json`；
- `execution-plan.json`；
- 三个干净 source context；
- 三个 buildx metadata 文件。

停止临时 registry 不会删除镜像数据或 candidate 文件：

```bash
USDB_E2E_LOCAL_IMAGE_WORK_DIR=/tmp/usdb-three-node-local-r1 \
scripts/usdb/prepare_local_release_images.sh stop
```

## 私有节点输入

从 USDB testnet bundle 的 `node.env.example` 创建测试专用文件，填写：

- 独占的 `BTC_NODE_DATA_HOST_DIR`；
- `BTC_RPCAUTH_HOST_FILE`；
- `BTC_RPC_USER` 和 `BTC_RPC_PASSWORD`；
- snapshot 配置；
- 可选的 `USDB_MINER_ADDRESS`。

镜像字段可以留空，运行器会从 manifest 写入每个临时 node env。生成文件权限固定为 `0600`，报告不会记录
RPC password。不要把正在被宿主机 `bitcoind` 使用的主网 datadir 交给容器；LevelDB lock 会使测试确定性失败。

## Preflight

CI manifest：

```bash
USDB_E2E_MANIFEST=/secure/release/usdb-testnet-v0-r1/release-manifest.json \
USDB_E2E_BASE_NODE_ENV=/secure/release/usdb-testnet-v0-r1/node.env \
scripts/usdb/run_usdb_three_node_release_e2e.sh preflight
```

本地 candidate：

```bash
LOCAL_ROOT=/tmp/usdb-three-node-local-r1

USDB_E2E_MANIFEST="$LOCAL_ROOT/release-manifest.json" \
USDB_E2E_COMPATIBILITY_LOCK="$LOCAL_ROOT/local-ci-revisions.json" \
USDB_E2E_BUNDLE_DIR="$LOCAL_ROOT/contexts/usdb/docker/networks/testnet-v0" \
USDB_E2E_IMAGE_MIRROR=127.0.0.1:5000 \
USDB_E2E_BASE_NODE_ENV=/secure/release/usdb-testnet-v0-r1/node.env \
scripts/usdb/run_usdb_three_node_release_e2e.sh preflight
```

Preflight 依次验证：manifest、compatibility lock、bundle、三个临时 node env、Compose 渲染、镜像
`RepoDigests` 和 source revision label。

## 三节点运行

将上述 `preflight` 改为 `run`。默认流程为：

1. 启动 digest-pinned Bitcoin Core 并等待 mainnet、txindex 和 tip readiness；
2. 启动 balance-history，等待 `consensus_ready=true`；
3. 启动 usdb-indexer，等待 `consensus_ready=true`；
4. 启动 node1 和 control-plane；
5. 启动并连接 node2；
6. 启动 late joiner node3，等待同步到 node1；
7. 使用原 datadir 重启 node2，并复核 chain ID、genesis 和同步高度；
8. 写入 `/tmp/usdb-three-node-release-e2e/report.json`。

Release conformance 默认要求挖矿。私有 node env 必须配置能够解析到 active standard pass 的
`USDB_MINER_ADDRESS`：

```bash
USDB_E2E_BASE_NODE_ENV=/secure/release/usdb-testnet-v0-r1/miner-node.env \
... \
scripts/usdb/run_usdb_three_node_release_e2e.sh run
```

运行器要求 node1 至少产出一个区块后才启动 late joiner，从而覆盖真实 block replay；矿工证不可用时测试
应 fail closed，而不是回退到 mock profile。

只检查容器组合、P2P 和 genesis identity 时可以显式设置 `USDB_E2E_ENABLE_MINING=0`。该模式的报告状态为
`bringup-only`，不能作为 release candidate 通过证据。

## 清理边界

默认清理仅作用于 `usdb-*-e2e*` project namespace：

- 删除三个测试 chain project 和测试 upstream project 的临时 named volumes；
- 停止测试 Bitcoin container，但不删除 bind-mounted Bitcoin datadir；
- 不操作正式节点 project、宿主机 `bitcoind` 或 snapshot 文件。

调试时可设置 `USDB_E2E_KEEP_RUNNING=1`。保留临时 Compose volumes 使用
`USDB_E2E_KEEP_DATA=1`。随后通过同一环境执行：

```bash
scripts/usdb/run_usdb_three_node_release_e2e.sh status
scripts/usdb/run_usdb_three_node_release_e2e.sh down
```

## 发布判定

本地模式通过只说明 manifest/schema、镜像组合和服务链 harness 可用。只有 CI 模式在干净部署主机上通过，
并同时验证 GitHub provenance、attestation、release approval 和下载来源后，才能作为 release candidate 的
发布证据。SourceDAO bootstrap/public bootstrap manifest 属于其后的启动验收层，不应与本跨仓镜像 manifest
混为同一 artifact。

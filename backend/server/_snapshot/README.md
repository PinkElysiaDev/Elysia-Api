# 源码快照占位

本目录是 `go:embed all:_snapshot` 的挂载点(下划线前缀让 go 工具链忽略此
目录,不参与编译与 ./... 遍历),内容由 `scripts/build-standalone.mjs` 在
构建前同步(backend 的 Go 源码与预置协议 JSON、webui 的 src),随二进制
分发给内置助手的 `elysia code` 命令查看。除本占位文件外的内容不进 git;
纯 `go build` 的开发构建里快照为空,命令会返回相应提示。

发布范围由 `scripts/source-snapshot.json` 逐项列出。新增或删除源码后，先暂存
文件，再运行 `node scripts/source-snapshot.mjs --update` 更新清单并一同提交。
没有 Git 元数据的源码包也使用这份清单，不递归扫描本地文件。运行配置、密钥、
日志及越过仓库边界的符号链接不能进入快照。

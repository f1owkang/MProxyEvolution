# zashboard overlay 维护说明

本目录是 MProxyEvolution 对 zashboard 的增量改造，**不直接修改 `upstream/zashboard` 子模块**。
每次构建都会：

1. 把 `upstream/zashboard` 复制到 `build/panel`；
2. 覆盖 `overlay/zashboard/` 中的新文件；
3. 应用 `patches/mproxy.patch`；
4. 执行字符串断言、`vue-tsc` 和 `vite build`。

因此 patch 应用失败会在 CI 的 **构建阶段直接失败**，不会带着错误的补丁发布。

## 固定基线

`patches/base.txt` 记录当前 patch 基于的 zashboard release tag 和 commit。
更新 zashboard 时请同时更新该文件。

## 上游更新流程

1. `sync-upstream` 工作流把 `upstream/zashboard` 更新到新 release tag，并创建 PR；
2. PR 触发 CI：
   - `scripts/dev-panel.sh --prepare` 会先验证 patch 能否应用；
   - 失败时 CI 红，PR 不会合并；
3. 维护者在本地修复 patch：

```bash
git submodule update --init upstream/zashboard
git -C upstream/zashboard checkout <new-tag>
scripts/dev-panel.sh --prepare      # 看 patch 失败位置
# 编辑 overlay/zashboard/patches/mproxy.patch
cd build/panel && pnpm type-check && pnpm build
```

4. 更新 `base.txt`，提交 patch 与新 submodule 指向。

## 为什么不会“悄悄错位”

- 只使用 **exact `git apply --check`**；fallback `patch` 设置 `--fuzz=0`，不接受模糊偏移；
- `dev-panel` 对关键改造做字符串断言（模块设置、沉浸式、MProxyEvolution 页面、boot 脚本等）；
- CI 还会执行 `vue-tsc` 与完整 `vite build`，类型/模板错误会暴露；
- 发版包由同一套构建脚本生成，人工无法绕过。

## 什么时候应该改用 fork

如果某次 zashboard 重构导致 patch 冲突频繁（例如 Settings 架构大改），应改用
`MProxyEvolution/zashboard` fork：把 overlay 变成 fork 上的普通 commit，sync bot 改为
`git merge upstream/tag`，这样可以用 Git 三方合并而不是文本 patch。当前改动量小，
patch + CI 仍是成本最低且可审计的方案。

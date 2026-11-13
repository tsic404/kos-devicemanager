# kos-devicemanager

KOS 系统应用项目：扩展包 — 节点硬件、设备、占用与陈旧标记。

- 设计：`tsic/kos-design` 仓的 `kos-devicemanager/`（范围、契约、验收、决策记录）

当前交付：设备页、节点硬件摘要与设备清单。页面只读并明示原因；展示项按来源
类型标注，对象源项带对象携带的采集时间。设备清单逐台给出四层事实与该条目的
采集时间，同一序列号只留最新一条；占用给出占用系统，采集给出占用对象时一并
展示，空闲显式标出；无设备的节点给出空态与原因。缺层告警、陈旧与无效标记、
写方法拒绝由后续子单补齐。

## 构建与自测

```sh
make verify      # gofmt + go vet
make test        # 单元测试与验收用例
make acceptance  # 只跑验收用例
make package     # 生成扩展包与摘要到 dist/
```

扩展包经 kos-sdk 打包：`manifest.json` 声明标识、版本、贡献与权限，摘要覆盖
清单与资产，第一方与第三方共用同一条打包路径。安装与卸载由扩展宿主执行，本仓
不修改宿主。

## 验收用例

| 用例 | 判据 |
|---|---|
| `tests/acceptance/DEVICE-TC-1` | 展示项带来源类型与观测时间；只读原因可见；无写入口 |
| `tests/acceptance/DEVICE-TC-2` | 停用后入口消失、直接调用被拒绝；其他页面不受影响 |
| `tests/acceptance/DEVICE-TC-3` | 节点身份、硬件概要与采集时间；采集缺失标注不可观察 |
| `tests/acceptance/DEVICE-TC-4` | 四层事实逐层可读；缺一层不影响其余三层与占用；同序列号只出现最新一条 |
| `tests/acceptance/DEVICE-TC-5` | 无设备节点的空态与原因；GPU 与普通设备同形 |
| `tests/acceptance/DEVICE-TC-6` | 占用给出占用系统与占用对象；空闲显式标出；无释放与改派入口 |
| `tests/acceptance/KOSD-TC-155` | 系统应用逐个装载与卸载；其余产品与内核对象不受影响 |
| `tests/acceptance/KOSD-TC-157` | 预装应用与第三方扩展同一通道装载、卸载与重装 |

// copyText 把文本写入剪贴板：优先标准异步剪贴板 API；面板常经
// http://<局域网IP> 访问（非安全上下文，navigator.clipboard 不存在）或
// 权限被拒时，降级为隐藏 textarea + execCommand。降级路径必须在用户
// 点击手势内同步执行才会生效，所以由调用方的 onClick 直接 await 本函数。
export async function copyText(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return
    } catch {
      // 权限拒绝等运行期失败：落入 execCommand 降级。
    }
  }
  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  // fixed + 透明：避免唤起键盘/滚动跳动，iOS Safari 需要 readonly 才不弹键盘。
  area.style.position = 'fixed'
  area.style.opacity = '0'
  document.body.appendChild(area)
  area.focus()
  area.select()
  try {
    if (!document.execCommand('copy')) {
      throw new Error('execCommand copy returned false')
    }
  } finally {
    area.remove()
  }
}

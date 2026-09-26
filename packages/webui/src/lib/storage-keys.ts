/** localStorage 键名的统一登记处：全站键都带 elysia-webui 前缀，集中在此防拼错与碰撞。 */
export const STORAGE_KEYS = {
  panelToken: 'elysia-webui.panel-token',
  theme: 'elysia-webui.theme',
  loginMotion: 'elysia-webui.login-motion',
  agentPanelWidth: 'elysia-webui.agent-panel-width',
  /** agent 输入的发送键模式：enter | ctrl-enter。 */
  agentSendKey: 'elysia-webui.agent-send-key',
} as const

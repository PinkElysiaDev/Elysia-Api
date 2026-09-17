import { Input } from '@/components/ui/input'
import { Seg } from '@/components/ui/seg'
import { SettingRow, SettingSection } from '@/components/ui/setting-card'
import { Switch } from '@/components/ui/switch'
import type { CustomProtocolConfig, CustomProtocolModels } from '@/lib/types'

const AUTH_MODES = [
  { value: 'inherit', label: '继承请求' },
  { value: 'bearer', label: 'Bearer' },
  { value: 'header', label: 'Header' },
  { value: 'query', label: 'Query' },
] as const

/** 模型拉取：声明发现端点后，引用本协议的模型源可开启自动拉取。 */
export function ModelsDiscoveryEditor({
  protocol,
  onChange,
}: {
  protocol: CustomProtocolConfig
  onChange: (protocol: CustomProtocolConfig) => void
}) {
  const models = protocol.models
  const update = (patch: Partial<CustomProtocolModels>) =>
    onChange({ ...protocol, models: { ...(models ?? { path: '', listPath: '' }), ...patch } })
  const authMode = models?.auth?.mode || 'inherit'

  return (
    <SettingSection
      title="模型拉取"
      description="声明模型列表发现端点后，引用本协议的模型源可开启自动拉取"
      action={
        <Switch
          checked={!!models}
          onCheckedChange={(checked) =>
            onChange({ ...protocol, models: checked ? { path: '', listPath: '' } : undefined })
          }
        />
      }
    >
      {!models ? (
        <p className="py-2 text-xs text-muted-foreground">
          未声明时，引用本协议的模型源只能手动维护模型列表。
        </p>
      ) : (
        <>
          <SettingRow label="请求方法">
            <Seg
              value={models.method || 'GET'}
              options={[
                { value: 'GET', label: 'GET' },
                { value: 'POST', label: 'POST' },
              ]}
              onChange={(value) => update({ method: value })}
            />
          </SettingRow>
          <SettingRow label="路径" required description="相对源 baseUrl，如 /v1/models">
            <Input
              className="w-64 font-mono text-xs"
              value={models.path}
              placeholder="/v1/models"
              onChange={(event) => update({ path: event.target.value })}
            />
          </SettingRow>
          <SettingRow label="列表路径" required description="点路径到模型数组，如 data 或 output.models">
            <Input
              className="w-64 font-mono text-xs"
              value={models.listPath}
              placeholder="data"
              onChange={(event) => update({ listPath: event.target.value })}
            />
          </SettingRow>
          <SettingRow label="ID 路径" description="元素内标识字段，默认 id">
            <Input
              className="w-64 font-mono text-xs"
              value={models.idPath ?? ''}
              placeholder="id"
              onChange={(event) => update({ idPath: event.target.value })}
            />
          </SettingRow>
          <SettingRow label="名称路径" description="元素内展示名字段，缺省用 ID">
            <Input
              className="w-64 font-mono text-xs"
              value={models.namePath ?? ''}
              placeholder="display_name"
              onChange={(event) => update({ namePath: event.target.value })}
            />
          </SettingRow>
          <SettingRow label="鉴权" description="拉取端点的鉴权方式，默认继承请求配置的 auth">
            <Seg
              value={authMode}
              options={AUTH_MODES.map((mode) => ({ value: mode.value, label: mode.label }))}
              onChange={(value) =>
                update({
                  auth:
                    value === 'inherit'
                      ? undefined
                      : {
                          mode: value,
                          ...(models?.auth ?? {}),
                        },
                })
              }
            />
          </SettingRow>
          {authMode === 'header' && (
            <SettingRow label="鉴权 Header" description="如 x-api-key">
              <Input
                className="w-64 font-mono text-xs"
                value={models.auth?.header ?? ''}
                placeholder="x-api-key"
                onChange={(event) => update({ auth: { mode: 'header', ...(models?.auth ?? {}), header: event.target.value } })}
              />
            </SettingRow>
          )}
          {authMode === 'query' && (
            <SettingRow label="鉴权参数名" description="如 apikey">
              <Input
                className="w-64 font-mono text-xs"
                value={models.auth?.query ?? ''}
                placeholder="apikey"
                onChange={(event) => update({ auth: { mode: 'query', ...(models?.auth ?? {}), query: event.target.value } })}
              />
            </SettingRow>
          )}
          <p className="text-2xs text-muted-foreground">
            更复杂的 headers / query 参数可在 JSON 页签声明；路径与列表路径为必填，保存时校验。
          </p>
        </>
      )}
    </SettingSection>
  )
}

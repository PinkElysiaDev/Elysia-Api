import { ProtocolProbe } from './v2-probe'
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import type { ProtocolDocument } from '@/lib/protocol-document'
import type { ProtocolSchema, VerificationReport } from '@/lib/protocol-v2'
import { cn } from '@/lib/utils'

/** Show evidence for the exact source and compiler revision. */
export function ProtocolVerification({ source, id, schema, document, mappingPrefix, report, verifiedSource }: {
  source: string
  id: string
  schema: ProtocolSchema
  document?: ProtocolDocument
  mappingPrefix: string
  report?: VerificationReport
  verifiedSource: string
}) {
  const isPackage = mappingPrefix === '/mapping'
  const mappings = document?.locate(`${mappingPrefix}/directions`)
  const directionKeys = [...(mappings?.children.keys() ?? [])]
  const declaredCapabilities = schema.capabilities.filter(
    (capability) => document?.read(`${mappingPrefix}/capabilities/${capability}`) === 'true',
  )

  return (
    <div className="space-y-4">
      <div
        role="status"
        className={cn(
          'inline-flex items-center rounded-full border px-3 py-1 text-xs font-medium',
          report && verifiedSource === source
            ? report.passed
              ? 'border-[color-mix(in_srgb,var(--jade)_26%,transparent)] bg-[color-mix(in_srgb,var(--jade)_9%,transparent)] text-jade'
              : 'border-destructive/30 bg-destructive/10 text-destructive'
            : 'border-border/80 bg-secondary/40 text-muted-foreground',
        )}
      >
        离线验证：
        {report && verifiedSource === source
          ? report.passed
            ? '通过'
            : '未通过'
          : '当前草稿尚未验证'}
      </div>

      <ProtocolProbe
        source={source}
        id={id}
        hash={verifiedSource === source ? report?.definitionHash : undefined}
      />

      <p className="text-xs text-muted-foreground">
        离线报告绑定定义、样例和编译器版本；修改内容后需要重新验证。真实上游验证是独立证据。
      </p>

      <div className="overflow-x-auto">
        <table className="w-full text-xs">
          <TableHeader>
            <TableRow className="border-b border-border/50 hover:bg-transparent">
              <TableHead className="py-2.5 pl-3">能力</TableHead>
              {directionKeys.map((name) => (
                <TableHead key={name} className="py-2.5 text-center font-mono">
                  {name}
                </TableHead>
              ))}
              <TableHead className="py-2.5 pr-3 text-right">样例证据</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody className="divide-y divide-border/30">
            {declaredCapabilities.map((capability) => (
              <TableRow key={capability} className="border-b-0">
                <TableCell className="py-2.5 pl-3 font-mono font-medium">
                  {capability}
                </TableCell>
                {directionKeys.map((name) => (
                  <TableCell key={name} className="py-2.5 text-center text-muted-foreground">
                    {document?.locate(`${mappingPrefix}/directions/${name}/capabilities`)
                      ? document.read(`${mappingPrefix}/directions/${name}/capabilities/${capability}`) ===
                        'true'
                        ? '支持'
                        : '—'
                      : '继承声明'}
                  </TableCell>
                ))}
                <TableCell className="py-2.5 pr-3 text-right">
                  {verifiedSource === source && report?.covered?.includes(capability) ? (
                    <span className="font-medium text-jade">已验证</span>
                  ) : (
                    <span className="text-muted-foreground">待验证</span>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </table>
      </div>

      {(report?.checks?.length ?? 0) > 0 && (
        <ul className="flex flex-wrap gap-2 text-xs">
          {report?.checks?.map((check, index) => (
            <li
              key={index}
              className={cn(
                'inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1 font-mono',
                check.passed
                  ? 'border-[color-mix(in_srgb,var(--jade)_26%,transparent)] bg-[color-mix(in_srgb,var(--jade)_8%,transparent)] text-jade'
                  : 'border-destructive/30 bg-destructive/10 text-destructive',
              )}
            >
              {check.passed ? '✓' : '×'} {check.sampleId} {check.direction}
            </li>
          ))}
        </ul>
      )}

      <details className="border-t border-border/40 pt-3.5 text-xs">
        <summary className="cursor-pointer font-medium text-foreground">当前引擎契约</summary>
        <pre className="mt-3 max-h-96 overflow-auto rounded-md bg-[var(--code-bg)] p-3 font-mono text-xs">
          {JSON.stringify(
            {
              engineFeatures: schema.engineFeatures,
              events: schema.events,
              ...(isPackage
                ? { package: schema.packageSchema, spec: schema.specSchema, mapping: schema.mappingSchema }
                : { definition: schema.definitionSchema }),
              semantic: schema.semanticSchema,
            },
            null,
            2,
          )}
        </pre>
      </details>
    </div>
  )
}

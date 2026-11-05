import { readFile, writeFile, mkdir } from 'node:fs/promises'
import { join, resolve } from 'node:path'

// Add measurement identity to the immutable A source with a Go test overlay.
// No production file or archived artifact is changed. Keep the exact overlay
// beside its binary so the instrumentation remains independently reproducible.
export async function instrumentLegacyExecution(root, output, runnerRoot) {
  const directory = join(output, 'collector-overlay')
  await mkdir(directory)
  const replace = (source, before, after) => {
    if (source.split(before).length !== 2) throw new Error(`Archived collector differs at ${before}`)
    return source.replace(before, after)
  }
  let helper = await readFile(join(runnerRoot, 'backend/server/conversion_execution_test.go'), 'utf8')
  helper = helper.slice(helper.indexOf('type conversionExecutionProof'))
  const start = helper.indexOf('func configureConversionExecution')
  helper = helper.slice(0, start) + helper.slice(helper.indexOf('func conversionExecutionIdentity'))
  helper = helper.slice(0, helper.indexOf('func TestConversionExecution'))
  helper += '\nfunc conversionExecution(t *testing.T) string {\n t.Helper()\n if protocol.CompilerVersion != "2.0.0-dev.101" || os.Getenv("ELYSIA_CONVERSION_HTTP_EXECUTION") != "legacy" { t.Fatal("reference A must use dev.101 legacy execution") }\n return "legacy"\n}\n'
  const providerHelper = await readFile(join(runnerRoot, 'backend/server/conversion_provider_test.go'), 'utf8')
  helper += '\n' + providerHelper.slice(providerHelper.indexOf('func configureConversionProvider'))
  const replacements = {}
  for (const name of ['conversion_http_performance_test.go', 'conversion_flamegraph_test.go']) {
    const original = resolve(root, 'backend/server', name)
    let source = (await readFile(original, 'utf8')).replaceAll('\r\n', '\n')
    if (name === 'conversion_http_performance_test.go') {
      source = replace(source, '"io"', '"io"\n "maps"')
      source = replace(source, '"github.com/elysia-api/backend/config"', '"github.com/elysia-api/backend/config"\n "github.com/elysia-api/backend/protocol"')
      source = replace(source, 'URL, Metrics string', 'URL, Metrics string\n Execution *conversionExecutionProof')
      source = replace(source, 'var loggingReview *loggingReviewGateway', 'var loggingReview *loggingReviewGateway\n var execution *conversionExecutionProof')
      source = replace(source, '"ELYSIA_CONVERSION_HTTP_UPSTREAM="+upstream)', '"ELYSIA_CONVERSION_HTTP_UPSTREAM="+upstream)\n configureConversionProvider(command, role)')
      source = replace(source, 'if role == "provider" {', 'if role == "provider" {\n execution = conversionProviderIdentity(t)')
      source = replace(source, 'handler := flameMatrixProvider(t, flameMatrixProtocol(t, targetID))', 'compiled := flameMatrixProtocol(t, targetID)\n execution.Packages[targetID] = compiled.Hash()\n handler := flameMatrixProvider(t, compiled)')
      source = replace(source, 'gateway := startTestServer(server.engine)', 'execution = conversionExecutionIdentity(t, server)\n gateway := startTestServer(server.engine)')
      source = replace(source, 'Metrics: metrics.URL, PID: os.Getpid()', 'Metrics: metrics.URL, PID: os.Getpid(), Execution: execution')
      source = replace(source, 'server.invalidateRouteCache()\n\t\t\treturn nil', 'server.invalidateRouteCache()\n\t\t\treturn checkReloadedConversionExecution(ctx, server, execution)')
      source = source.replaceAll('metrics(gateway.Metrics, "reset")', 'metrics(gateway.Metrics, "reset-without-gc")')
      source += '\n' + helper
    } else {
      source = replace(source, 'provider := startConversionHTTPChild(t, "provider", "")', 'provider := startConversionHTTPChild(t, "provider", "")\n writeConversionProviderIdentity(t, directory, provider)')
      source = replace(source, 'gateway := startConversionHTTPChild(t, "gateway", provider.URL)', `gateway := startConversionHTTPChild(t, "gateway", provider.URL)
 if gateway.Execution == nil || gateway.Execution.Commit != AppCommit || gateway.Execution.Execution != "legacy" { t.Fatal("reference A execution identity mismatch") }
 proof, err := json.MarshalIndent(gateway.Execution, "", "  ")
 if err != nil { t.Fatal(err) }
 if err := os.WriteFile(filepath.Join(directory, "execution.json"), proof, 0600); err != nil { t.Fatal(err) }
 t.Logf("execution: %s", proof)`)
    }
    const patched = join(directory, name)
    await writeFile(patched, source)
    replacements[original] = patched
  }
  const overlay = join(directory, 'overlay.json')
  await writeFile(overlay, JSON.stringify({ Replace: replacements }, null, 2) + '\n')
  return overlay
}

/** Reads a credential without echoing it or putting it in process arguments. */
export async function readCredential() {
  if (process.env.ELYSIA_VERIFY_API_KEY) return process.env.ELYSIA_VERIFY_API_KEY
  process.stdout.write('Temporary API credential (hidden input): ')
  const input = process.stdin
  if (input.isTTY) input.setRawMode(true)
  input.resume()
  return new Promise((resolve, reject) => {
    let secret = ''
    function finish(error) {
      input.off('data', onData)
      input.off('end', onEnd)
      if (input.isTTY) input.setRawMode(false)
      input.pause()
      process.stdout.write('\n')
      if (error) reject(error)
      else if (!secret) reject(new Error('A temporary credential is required'))
      else resolve(secret)
    }
    function onEnd() { finish() }
    function onData(chunk) {
      for (const character of chunk.toString()) {
        if (character === '\u0003') return finish(new Error('Credential input cancelled'))
        if (character === '\r' || character === '\n') return finish()
        if (character === '\u007f' || character === '\b') secret = secret.slice(0, -1)
        else secret += character
      }
    }
    input.on('data', onData)
    input.once('end', onEnd)
  })
}

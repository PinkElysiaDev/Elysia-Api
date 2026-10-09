import { pathToFileURL } from 'node:url';
import assert from 'node:assert/strict';

const { default: OpenAI } = await import(pathToFileURL(process.argv[2]).href);
const client = new OpenAI({ apiKey: 'gateway-test-token', baseURL: `${process.argv[3]}/gateway/openai-chat-completions`, maxRetries: 0 });
const tools = [{ type: 'function', function: { name: 'lookup', parameters: { type: 'object', properties: { value: { type: 'string' } }, required: ['value'] } } }];
for (const stream of [false, true]) {
  const messages = [{ role: 'user', content: 'Read the local fixture.' }];
  const headers = { 'x-elysia-session-id': `openai-sdk-${stream}` };
  const first = stream
    ? await client.chat.completions.stream({ model: 'group', stream_options: { include_usage: true }, messages, tools }, { headers }).finalChatCompletion()
    : await client.chat.completions.create({ model: 'group', messages, tools }, { headers });
  assert.equal(first.choices[0].message.tool_calls.length, 1);
  assert.equal(first.usage.total_tokens, 5);
  const message = first.choices[0].message;
  // Project the SDK's enriched parsed result back to the Chat request schema.
  // Deliberately omit the gateway extension in the streaming case to exercise
  // clients that keep only standard fields, using exact persistent recovery.
  messages.push(stream ? {
    role: 'assistant', content: message.content,
    tool_calls: message.tool_calls.map(({ id, type, function: fn }) => ({ id, type, function: { name: fn.name, arguments: fn.arguments } })),
  } : message);
  messages.push({ role: 'tool', tool_call_id: 'client-call', content: 'LOCAL_CLIENT_FIXTURE' });
  // SDK stream aggregation may discard unknown extensions. A stable session
  // supplies the independently tested exact persistent recovery channel.
  const second = await client.chat.completions.create({ model: 'group', messages, tools }, { headers });
  assert.equal(second.choices[0].message.content, 'LOCAL_CLIENT_OK');
}
// Responses include is a client output preference, not a Gemini parameter.
const responses = new OpenAI({ apiKey: 'gateway-test-token', baseURL: `${process.argv[3]}/gateway/openai-responses`, maxRetries: 0 });
for (const stream of [false, true]) {
  const input = [{ role: 'user', content: 'Read the local fixture.' }];
  const headers = { 'x-elysia-session-id': `responses-sdk-${stream}` };
  const options = { model: 'group', input, include: ['reasoning.encrypted_content'], tools: [{ type: 'function', name: 'lookup', parameters: tools[0].function.parameters }] };
  let first;
  if (stream) {
    for await (const event of await responses.responses.create({ ...options, stream: true }, { headers })) {
      if (event.type === 'response.completed') first = event.response;
    }
  } else first = await responses.responses.create(options, { headers });
  assert(first, 'Responses stream did not complete');
  const call = first.output.find((item) => item.type === 'function_call');
  assert(call, 'Responses tool call missing');
  assert(first.output.some((item) => item.type === 'reasoning' && item.encrypted_content?.startsWith('elysia-continuation.v1.')));
  input.push({ type: 'function_call', name: call.name, call_id: call.call_id, arguments: call.arguments });
  if (!stream) input.push(...first.output.filter((item) => item.type === 'reasoning'));
  input.push({ type: 'function_call_output', call_id: call.call_id, output: 'LOCAL_CLIENT_FIXTURE' });
  const second = await responses.responses.create({ ...options, input }, { headers });
  assert.equal(second.output_text, 'LOCAL_CLIENT_OK');
}
console.log('LOCAL_CLIENT_OK');

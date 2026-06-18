import React, { useEffect, useRef, useState, Suspense, lazy } from 'react';
import { Button, Input, List, Spin, Alert, Card, Tag, Typography, Select } from 'antd';
import { SendOutlined, StopOutlined } from '@ant-design/icons';
import { HttpAgent } from '@ag-ui/client';
import type { AgentSubscriber, ToolCall, Message, Tool } from '@ag-ui/client';
import { apiHeaders, apiPost, apiGet } from '../api/core';
import type { HostRecord } from '../HostPicker';

const AiMarkdown = lazy(async () => import('../AiMarkdown').then((m) => ({ default: m.AiMarkdown })));

const { Text } = Typography;

const TOOLS: Tool[] = [
  {
    name: 'list_backends',
    description: 'List all available infrastructure providers and backend names from the configuration file.',
    parameters: {
      type: 'object',
      properties: {},
      required: [],
    },
  },
  {
    name: 'search_hosts',
    description: 'Search infrastructure hosts by name, provider, or backend.',
    parameters: {
      type: 'object',
      properties: { 
        name: { type: 'string', description: 'Substring to match against host names.' },
        providers: { type: 'string', description: 'Comma-separated list of providers (e.g. docker,aws,proxmox).' },
        backends: { type: 'string', description: 'Comma-separated list of backend names.' },
      },
      required: [],
    },
  },
  {
    name: 'validate_recipe',
    description: 'Validate a Honey CUE recipe.',
    parameters: {
      type: 'object',
      properties: { recipe_content_raw: { type: 'string' } },
      required: ['recipe_content_raw'],
    },
  },
] as any[]; // cast to any to bypass TS error if Tool expects OpenAI format

async function executeTool(tc: ToolCall): Promise<string> {
  const args = JSON.parse(tc.function.arguments ?? '{}');
  const toolName = tc.function.name.replace(/^default_api:/, '');
  
  if (toolName === 'list_backends') {
    const res = await apiGet('/api/v1/backends');
    if (!res.ok) {
        const j = (await res.json().catch(() => ({}))) as { error?: string };
        return `Failed to list backends: ${j.error || res.statusText}`;
    }
    const data = await res.json() as { backends?: { kind: string; name: string; hint: string }[] };
    const backends = data.backends || [];
    if (!backends.length) return 'No backends found in configuration.';
    return backends.map(b => `- Provider: ${b.kind} | Backend: ${b.name} | Hint: ${b.hint}`).join('\n');
  }

  if (toolName === 'search_hosts') {
    const reqBody: any = { name: args.name ?? '' };
    if (args.providers) reqBody.providers = args.providers;
    if (args.backends) reqBody.backends = args.backends;
    
    const res = await apiPost('/api/v1/search', reqBody);
    if (!res.ok) {
        const j = (await res.json().catch(() => ({}))) as { error?: string };
        return `Failed to search: ${j.error || res.statusText}`;
    }
    const data = await res.json() as { records?: HostRecord[] };
    const hosts = data.records || [];
    if (!hosts.length) return 'No hosts found.';
    return hosts.map((h: HostRecord) => `${h.name} (${h.primary_ip}, ${h.provider})`).join('\n');
  }
  if (toolName === 'validate_recipe') {
    const res = await apiPost('/api/v1/recipes/validate-content', { recipe_content_raw: args.recipe_content_raw });
    if (!res.ok) {
        const j = (await res.json().catch(() => ({}))) as { error?: string };
        return `Failed to validate: ${j.error || res.statusText}`;
    }
    const data = await res.json() as { errors?: string[] };
    return data.errors?.length ? `Errors:\n${data.errors.join('\n')}` : 'Recipe is valid.';
  }
  return `Unknown tool: ${toolName}`;
}

export function AgentTab({ assistAvailable }: { assistAvailable: boolean }) {
  const [messages, setMessages] = useState<Message[]>([]);
  const [streaming, setStreaming] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [inputText, setInputText] = useState('');
  
  const [models, setModels] = useState<string[]>([]);
  const [selectedModel, setSelectedModel] = useState<string>('');
  const [modelsLoading, setModelsLoading] = useState(false);
  
  const agentRef = useRef<HttpAgent | null>(null);

  useEffect(() => {
    if (!assistAvailable) return undefined;
    let cancelled = false;
    setModelsLoading(true);
    void (async () => {
      try {
        const r = await apiGet('/api/v1/terminal-assist/models');
        const j = (await r.json().catch(() => ({}))) as { models?: string[]; error?: string };
        if (cancelled) return;
        
        if (!r.ok) {
          console.error('Failed to load models:', j.error || r.statusText);
          return;
        }
        
        const list = Array.isArray(j.models) ? j.models : [];
        setModels(list);
        if (list.length > 0) {
          const preferred = list.find(m => m.includes('gemini-1.5-pro') || m.includes('gpt-4o') || m.includes('claude-3-5-sonnet'));
          setSelectedModel(preferred || list[0]);
        }
      } catch (e) {
        if (!cancelled) {
          console.error('Failed to load models', e);
        }
      } finally {
        if (!cancelled) {
          setModelsLoading(false);
        }
      }
    })();
      
    return () => { cancelled = true; };
  }, [assistAvailable]);

  useEffect(() => {
    const url = new URL('/api/v1/agent', window.location.href);
    agentRef.current = new HttpAgent({
      url: url.toString(),
      headers: apiHeaders() as Record<string, string>,
    });
  }, []);

  const handleSend = async (text: string) => {
    if (!text.trim() || !agentRef.current) return;
    
    setStreaming(true);
    setError(null);
    setInputText('');
    
    const userMsg: Message = { role: 'user', content: text };
    const allMessages = [...messages, userMsg];
    setMessages(allMessages);
    agentRef.current.messages = allMessages;
    
    const subscriber: AgentSubscriber = {
      onNewMessage: ({ message: msg }) => setMessages(prev => [...prev.filter(m => m.id !== msg.id), msg]),
      onMessagesChanged: ({ messages: msgs }) => setMessages(msgs),
      onNewToolCall: async ({ toolCall, messages: msgs, agent }) => {
        try {
            const result = await executeTool(toolCall);
            const toolMsg: Message = { role: 'tool', toolCallId: toolCall.id, content: result };
            agent.messages = [...msgs, toolMsg];
            // Ensure the URL still has the correct model
            const url = new URL('/api/v1/agent', window.location.href);
            url.searchParams.set('model', selectedModel);
            if (agent instanceof HttpAgent) {
              agent.url = url.toString();
            }
            await agent.runAgent({ messages: agent.messages, tools: TOOLS }, subscriber);
        } catch (e: any) {
            setError(`Tool error: ${e.message}`);
            setStreaming(false);
        }
      },
      onRunFailed: ({ error: err }) => { 
        setError(err.message); 
        setStreaming(false); 
      },
      onRunFinalized: () => {
        setStreaming(false);
      },
    };

    try {
      const url = new URL('/api/v1/agent', window.location.href);
      url.searchParams.set('model', selectedModel);
      agentRef.current.url = url.toString();
      await agentRef.current.runAgent({ messages: allMessages, tools: TOOLS }, subscriber);
    } catch (e: any) {
      setError(`Run error: ${e.message}`);
      setStreaming(false);
    }
  };

  const handleStop = () => {
    agentRef.current?.abortRun();
    setStreaming(false);
  };

  if (!assistAvailable) {
    return (
      <div style={{ padding: 24, maxWidth: 600, margin: '0 auto' }}>
        <Alert
          message="AI Agent Not Configured"
          description="Please configure OPENAI_API_KEY environment variable to use the AI Agent."
          type="warning"
          showIcon
        />
      </div>
    );
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', maxWidth: 800, margin: '0 auto' }}>
      <div style={{ flex: 1, overflow: 'auto', padding: 16 }}>
        <List
          dataSource={messages}
          renderItem={(msg) => {
            if (msg.role === 'user') {
              return (
                <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 16 }}>
                  <div style={{ background: '#177ddc', color: 'white', padding: '8px 12px', borderRadius: 8, maxWidth: '80%' }}>
                    {msg.content}
                  </div>
                </div>
              );
            }
            if (msg.role === 'tool') {
              return (
                <div style={{ display: 'flex', justifyContent: 'flex-start', marginBottom: 16 }}>
                  <Card size="small" style={{ background: '#141414', border: '1px solid #303030' }}>
                    <Tag color="purple">Tool Result</Tag>
                    <pre style={{ margin: 0, marginTop: 8, fontSize: '12px', color: '#a6a6a6' }}>
                      {msg.content}
                    </pre>
                  </Card>
                </div>
              );
            }
            if (msg.role === 'assistant') {
              if (msg.toolCalls && msg.toolCalls.length > 0) {
                 return (
                    <div style={{ display: 'flex', justifyContent: 'flex-start', marginBottom: 16 }}>
                      <Card size="small" style={{ background: '#1f1f1f', border: '1px solid #434343' }}>
                        {msg.toolCalls.map(tc => (
                            <div key={tc.id}>
                                <Tag color="blue">{tc.function.name}</Tag>
                                <Text type="secondary" style={{ fontSize: '12px' }}>{tc.function.arguments}</Text>
                            </div>
                        ))}
                      </Card>
                    </div>
                 );
              }
              return (
                <div style={{ display: 'flex', justifyContent: 'flex-start', marginBottom: 16 }}>
                  <div style={{ background: '#1f1f1f', padding: '12px 16px', borderRadius: 8, maxWidth: '90%' }}>
                    <Suspense fallback={<Spin size="small" />}>
                      <AiMarkdown text={msg.content || ''} />
                    </Suspense>
                  </div>
                </div>
              );
            }
            return null;
          }}
        />
        {streaming && !messages.find(m => m.role === 'assistant' && !m.content && !m.toolCalls?.length) && (
          <div style={{ display: 'flex', justifyContent: 'flex-start', marginBottom: 16 }}>
             <Spin size="small" />
          </div>
        )}
        {error && (
          <Alert message="Agent Error" description={error} type="error" showIcon style={{ marginBottom: 16 }} />
        )}
      </div>
      <div style={{ padding: 16, borderTop: '1px solid #303030' }}>
        <div style={{ marginBottom: 12 }}>
          <Select
            style={{ minWidth: 200 }}
            size="small"
            value={selectedModel}
            onChange={setSelectedModel}
            loading={modelsLoading}
            options={models.map(m => ({ label: m, value: m }))}
            disabled={streaming}
            placeholder="Select a model..."
          />
        </div>
        <div style={{ display: 'flex', gap: 8 }}>
          <Input.TextArea
            value={inputText}
            onChange={e => setInputText(e.target.value)}
            onPressEnter={(e) => {
              if (!e.shiftKey) {
                e.preventDefault();
                handleSend(inputText);
              }
            }}
            placeholder="Ask the AI agent..."
            autoSize={{ minRows: 1, maxRows: 4 }}
            disabled={streaming}
          />
          {streaming ? (
            <Button type="primary" danger icon={<StopOutlined />} onClick={handleStop}>Stop</Button>
          ) : (
            <Button type="primary" icon={<SendOutlined />} onClick={() => handleSend(inputText)} disabled={!inputText.trim()}>Send</Button>
          )}
        </div>
      </div>
    </div>
  );
}

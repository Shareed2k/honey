import React, { useEffect, useRef, useState } from 'react';
import { Button, Input, List, Spin, Alert, Card, Tag, Typography } from 'antd';
import { SendOutlined, StopOutlined } from '@ant-design/icons';
import { HttpAgent } from '@ag-ui/client';
import type { AgentSubscriber, ToolCall, Message, Tool } from '@ag-ui/client';
import { apiHeaders, apiPost } from '../api';
import type { HostRecord } from '../HostPicker';
import { AiMarkdown } from '../AiMarkdown';

const { Text } = Typography;

const TOOLS: Tool[] = [
  {
    type: 'function',
    function: {
      name: 'search_hosts',
      description: 'Search infrastructure hosts by name.',
      parameters: {
        type: 'object',
        properties: { name: { type: 'string' } },
        required: [],
      },
    },
  },
  {
    type: 'function',
    function: {
      name: 'validate_recipe',
      description: 'Validate a Honey CUE recipe.',
      parameters: {
        type: 'object',
        properties: { recipe_content_raw: { type: 'string' } },
        required: ['recipe_content_raw'],
      },
    },
  },
];

async function executeTool(tc: ToolCall): Promise<string> {
  const args = JSON.parse(tc.function.arguments ?? '{}');
  if (tc.function.name === 'search_hosts') {
    const res = await apiPost('/api/v1/search', { name: args.name ?? '' });
    if (!res.ok) {
        return `Failed to search: ${res.statusText}`;
    }
    const data = await res.json();
    const hosts = (data.records ?? []) as HostRecord[];
    if (!hosts.length) return 'No hosts found.';
    return hosts.map((h: HostRecord) => `${h.Name} (${h.PrimaryIP}, ${h.Provider})`).join('\n');
  }
  if (tc.function.name === 'validate_recipe') {
    const res = await apiPost('/api/v1/recipes/validate-content', { recipe_content_raw: args.recipe_content_raw });
    if (!res.ok) {
        return `Failed to validate: ${res.statusText}`;
    }
    const data = await res.json();
    return data.errors?.length ? `Errors:\n${data.errors.join('\n')}` : 'Recipe is valid.';
  }
  return `Unknown tool: ${tc.function.name}`;
}

export function AgentTab({ assistAvailable }: { assistAvailable: boolean }) {
  const [messages, setMessages] = useState<Message[]>([]);
  const [streaming, setStreaming] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [inputText, setInputText] = useState('');
  
  const agentRef = useRef<HttpAgent | null>(null);

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
      onNewMessage: (msg) => setMessages(prev => [...prev.filter(m => m.id !== msg.id), msg]),
      onMessagesChanged: (msgs) => setMessages(msgs),
      onNewToolCall: async (toolCall, msgs, _state, agent) => {
        try {
            const result = await executeTool(toolCall);
            const toolMsg: Message = { role: 'tool', toolCallId: toolCall.id, content: result };
            agent.messages = [...msgs, toolMsg];
            await agent.runAgent({ messages: agent.messages, tools: TOOLS }, subscriber);
        } catch (e: any) {
            setError(`Tool error: ${e.message}`);
            setStreaming(false);
        }
      },
      onRunFailed: (err) => { 
        setError(err.message); 
        setStreaming(false); 
      },
      onRunFinalized: () => {
        setStreaming(false);
      },
    };

    try {
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
                    <AiMarkdown text={msg.content || ''} />
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

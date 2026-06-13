import React, { useState } from 'react';
import { Button, Input, Space, Popover, message } from 'antd';
import { DeleteOutlined, PlusOutlined, LockOutlined } from '@ant-design/icons';
import { encryptSecret } from '../api';

type Props = {
  value: Record<string, string>;
  onChange: (val: Record<string, string>) => void;
};

export default function KeyValueEditor({ value = {}, onChange }: Props) {
  const entries = Object.entries(value || {});
  const [encryptingKey, setEncryptingKey] = useState<string | null>(null);
  const [plaintext, setPlaintext] = useState('');
  const [isEncrypting, setIsEncrypting] = useState(false);

  const updateKey = (oldKey: string, newKey: string) => {
    const next = { ...value };
    const val = next[oldKey];
    delete next[oldKey];
    next[newKey] = val;
    onChange(next);
  };

  const updateValue = (key: string, val: string) => {
    onChange({ ...value, [key]: val });
  };

  const removeRow = (key: string) => {
    const next = { ...value };
    delete next[key];
    onChange(next);
  };

  const addRow = () => {
    const newKey = `KEY_${entries.length + 1}`;
    onChange({ ...value, [newKey]: '' });
  };

  const handleEncrypt = async (key: string) => {
    if (!plaintext.trim()) return;
    setIsEncrypting(true);
    try {
      const encrypted = await encryptSecret(plaintext);
      updateValue(key, encrypted);
      setEncryptingKey(null);
      setPlaintext('');
      message.success('Secret encrypted successfully');
    } catch (err: unknown) {
      message.error('Encryption failed: ' + (err instanceof Error ? err.message : String(err)));
    } finally {
      setIsEncrypting(false);
    }
  };

  const renderEncryptContent = (key: string) => (
    <Space.Compact style={{ width: '100%' }}>
      <Input 
        type="password" 
        placeholder="Plaintext secret" 
        value={plaintext} 
        onChange={(e) => setPlaintext(e.target.value)} 
        onPressEnter={() => handleEncrypt(key)}
      />
      <Button type="primary" loading={isEncrypting} onClick={() => handleEncrypt(key)}>
        Encrypt
      </Button>
    </Space.Compact>
  );

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      {entries.map(([k, v]) => (
        <Space key={k} style={{ display: 'flex', marginBottom: 8 }} align="baseline">
          <Input placeholder="Key" value={k} onChange={(e) => updateKey(k, e.target.value)} />
          <Input placeholder="Value" value={v} onChange={(e) => updateValue(k, e.target.value)} />
          <Popover 
            title="Encrypt Secret" 
            trigger="click" 
            open={encryptingKey === k} 
            onOpenChange={(open) => {
              if (open) {
                setEncryptingKey(k);
                setPlaintext('');
              } else {
                setEncryptingKey(null);
              }
            }}
            content={renderEncryptContent(k)}
          >
            <Button icon={<LockOutlined />} title="Encrypt Value" />
          </Popover>
          <Button danger icon={<DeleteOutlined />} onClick={() => removeRow(k)} />
        </Space>
      ))}
      <Button type="dashed" onClick={addRow} block icon={<PlusOutlined />}>
        Add Variable
      </Button>
    </div>
  );
}

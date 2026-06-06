import React from 'react';
import { Handle, Position } from '@xyflow/react';

export default function CustomStepNode({ data }: any) {
  const error = data?.error;
  const isOk = !error;
  return (
    <div
      style={{
        padding: '10px 20px',
        borderRadius: '4px',
        background: isOk ? '#0d1117' : '#2d1114',
        border: `1px solid ${isOk ? '#30363d' : '#f85149'}`,
        color: '#c9d1d9',
        minWidth: 150,
      }}
    >
      <Handle type="target" position={Position.Left} style={{ background: '#58a6ff' }} />
      <div>
        <strong>{data?.label}</strong>
        {data?.wave ? <span style={{ fontSize: '10px', color: '#8b949e', marginLeft: 8 }}>Wave {data.wave}</span> : null}
      </div>
      <div style={{ fontSize: '12px', color: '#8b949e', marginTop: '4px' }}>
        Kind: {data?.kind}
      </div>
      <div style={{ fontSize: '12px', color: '#8b949e' }}>
        Host: {data?.host}
      </div>
      {error && (
        <div style={{ fontSize: '12px', color: '#f85149', marginTop: '4px', maxWidth: '200px', wordWrap: 'break-word' }}>
          {error}
        </div>
      )}
      <Handle type="source" position={Position.Right} style={{ background: '#58a6ff' }} />
    </div>
  );
}

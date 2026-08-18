import { useEffect, useState, type ReactNode } from 'react';
import { Button, Checkbox, Input, Modal, Space, Switch, Typography } from 'antd';
import type { InterceptOptions } from '../api/intercept';
import type { HostRecord } from '../HostPicker';

export type InterceptModalProps = {
  record: HostRecord | null;
  open: boolean;
  onClose: () => void;
  onLaunch: (cfg: InterceptOptions) => void;
};

function modeLabel(name: string, desc: string): ReactNode {
  return (
    <span>
      {name}{' '}
      <Typography.Text type="secondary" style={{ fontSize: '0.78rem' }}>— {desc}</Typography.Text>
    </span>
  );
}

// Incoming is intentionally absent: it forwards the pod's inbound traffic to a
// local host:port, which the browser terminal has no way to specify -- it is a
// CLI-only mode (honey intercept --mode incoming --target host:port).
const MODE_OPTIONS: { label: ReactNode; value: string }[] = [
  { value: 'egress', label: modeLabel('Egress', "the pod's network: cluster DNS and service IPs") },
  { value: 'files', label: modeLabel('Files', "read the pod's filesystem") },
  { value: 'env', label: modeLabel('Env', "the pod's environment variables") },
];

const DEFAULT_MODES = ['egress'];
const DEFAULT_COMMAND = '/bin/sh';

export function InterceptModal({ record, open, onClose, onLaunch }: InterceptModalProps) {
  const [modes, setModes] = useState<string[]>(DEFAULT_MODES);
  const [udp, setUdp] = useState(false);
  const [command, setCommand] = useState(DEFAULT_COMMAND);

  // Reset to defaults every time the modal is opened for a (possibly new) target.
  useEffect(() => {
    if (open) {
      setModes(DEFAULT_MODES);
      setUdp(false);
      setCommand(DEFAULT_COMMAND);
    }
  }, [open, record]);

  const onSubmit = () => {
    const cmd = command.trim();
    onLaunch({
      modes,
      udp,
      command: cmd ? cmd.split(/\s+/) : undefined,
    });
  };

  return (
    <Modal
      open={open}
      onCancel={onClose}
      title={record ? `Intercept — ${record.name}` : 'Intercept'}
      width="min(420px, 94vw)"
      destroyOnHidden
      footer={[
        <Button key="cancel" onClick={onClose}>
          Cancel
        </Button>,
        <Button key="launch" type="primary" disabled={modes.length === 0} onClick={onSubmit}>
          Start intercept
        </Button>,
      ]}
    >
      {record && (
        <Space direction="vertical" size={12} style={{ width: '100%' }}>
          <Typography.Text type="secondary" style={{ fontSize: '0.85rem' }}>
            Injects an ephemeral debug container into <strong>{record.name}</strong> and streams a shell
            over the browser terminal.
          </Typography.Text>
          <div>
            <Typography.Text id="intercept-modes-label" style={{ display: 'block', marginBottom: 4 }}>
              Modes
            </Typography.Text>
            <Checkbox.Group
              aria-labelledby="intercept-modes-label"
              options={MODE_OPTIONS}
              value={modes}
              onChange={(vals) => setModes(vals as string[])}
              style={{ display: 'flex', flexDirection: 'column', gap: 6 }}
            />
          </div>
          <div>
            <Space align="center">
              <Switch checked={udp} onChange={setUdp} />
              <Typography.Text>UDP</Typography.Text>
            </Space>
          </div>
          <div>
            <label htmlFor="intercept-command" style={{ display: 'block', marginBottom: 4 }}>
              <Typography.Text>Command (optional)</Typography.Text>
            </label>
            <Input
              id="intercept-command"
              value={command}
              onChange={(e) => setCommand(e.target.value)}
              placeholder={DEFAULT_COMMAND}
            />
            <Typography.Text type="secondary" style={{ display: 'block', marginTop: 4, fontSize: '0.78rem' }}>
              Runs on this machine; its network, files, and env come from the pod.
            </Typography.Text>
          </div>
          <Typography.Text type="secondary" style={{ fontSize: '0.78rem' }}>
            The session survives a browser refresh — reattach from the pod row or the
            intercepts list. It ends when you Stop it, close the terminal tab, or the
            shell exits.
          </Typography.Text>
        </Space>
      )}
    </Modal>
  );
}

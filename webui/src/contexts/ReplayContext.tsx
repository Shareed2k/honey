import { createContext, useContext, useState, useCallback, type ReactNode } from 'react';
import { Modal, Button, Spin, Alert, message } from 'antd';
import type { HostRecord } from '../HostPicker';
import { recordKey } from '../HostPicker';
import type { RecordingListEntry, RecordingsListResponse } from '../api/types/recordings';
import { fetchRecordingsForHost, fetchRecordingsList, fetchRecordingEvents, fetchRecordingsFailedHosts } from '../api/recordings';
import { SessionReplayModal } from '../SessionReplayModal';
import { useHostSelection } from './HostSelectionContext';
import { useNavigation } from './NavigationContext';
import { useAppContext } from './AppContext';

interface ReplayContextType {
  openReplayModal: (rec: HostRecord) => void;
  openReplayAllRecordings: () => void;
}

const ReplayContext = createContext<ReplayContextType | null>(null);

export function ReplayProvider({ children }: { children: ReactNode }) {
  const { setRecords, setSelectedKeys } = useHostSelection();
  const { setTab } = useNavigation();
  const { meta } = useAppContext();

  const [replayRecord, setReplayRecord] = useState<HostRecord | null>(null);
  const [replayItems, setReplayItems] = useState<RecordingListEntry[]>([]);
  const [replayListMeta, setReplayListMeta] = useState<Pick<
    RecordingsListResponse,
    'file_count' | 'total_bytes' | 'retention'
  > | null>(null);
  const [replayErr, setReplayErr] = useState<string | null>(null);

  const openReplayModal = useCallback(async (rec: HostRecord) => {
    setReplayErr(null);
    setReplayRecord(rec);
    setReplayItems([]);
    try {
      const items = await fetchRecordingsForHost({
        provider: rec.provider,
        host_name: rec.name,
        host_ip: rec.primary_ip,
      });
      setReplayItems(items);
      if (items.length === 0) {
        setReplayErr('No recordings found for this host.');
      }
    } catch (e) {
      setReplayErr(e instanceof Error ? e.message : String(e));
    }
  }, []);

  const openReplayAllRecordings = useCallback(async () => {
    const placeholder: HostRecord = { provider: '', name: 'All recordings', primary_ip: '' };
    setReplayErr(null);
    setReplayRecord(placeholder);
    setReplayItems([]);
    setReplayListMeta(null);
    try {
      const resp = await fetchRecordingsList();
      setReplayItems(resp.items);
      setReplayListMeta({
        file_count: resp.file_count,
        total_bytes: resp.total_bytes,
        retention: resp.retention,
      });
      if (resp.items.length === 0) {
        setReplayErr('No files in record-dir yet.');
      }
    } catch (e) {
      setReplayErr(e instanceof Error ? e.message : String(e));
    }
  }, []);

  const handleRetryFailed = async (fileName: string) => {
    try {
      const events = await fetchRecordingEvents(fileName);
      const metaEv = events.find((e) => e.type === 'recipe-meta');
      const recipePath = (metaEv?.result as Record<string, unknown>)?.recipe_path as string | undefined;
      if (!recipePath) {
        message.warning('Recording does not contain recipe metadata');
        return;
      }
      const failedHosts = await fetchRecordingsFailedHosts(fileName);
      if (failedHosts.length === 0) {
        message.info('No failed hosts found in this recording');
        return;
      }
      
      setReplayRecord(null);
      setTab('studio');
      
      setRecords((prev) => {
        const merged = [...prev];
        for (const fh of failedHosts) {
          if (!merged.some(r => recordKey(r) === recordKey(fh))) {
            merged.push(fh);
          }
        }
        return merged;
      });
      
      const nextKeys: Record<string, boolean> = {};
      for (const h of failedHosts) nextKeys[recordKey(h)] = true;
      setSelectedKeys(nextKeys);
      
      message.success(`Loaded ${failedHosts.length} failed hosts for retry`);
    } catch (e) {
      message.error('Failed to prepare retry: ' + (e instanceof Error ? e.message : String(e)));
    }
  };

  return (
    <ReplayContext.Provider value={{ openReplayModal, openReplayAllRecordings }}>
      {children}
      {replayRecord ? (
        replayItems.length > 0 ? (
          <SessionReplayModal
            record={replayRecord}
            recordings={replayItems}
            listStats={replayListMeta ? { file_count: replayListMeta.file_count, total_bytes: replayListMeta.total_bytes } : undefined}
            retention={replayListMeta?.retention}
            assistAvailable={!!meta?.terminal_assist_available}
            onRecordingsChange={() => void openReplayAllRecordings()}
            onClose={() => { setReplayRecord(null); setReplayListMeta(null); }}
            onRetryFailed={handleRetryFailed}
          />
        ) : (
          <Modal maskClosable={false} open
            title="Session replay"
            onCancel={() => setReplayRecord(null)}
            footer={<Button onClick={() => setReplayRecord(null)}>Close</Button>}
            width="min(520px, 94vw)"
          >
            {replayErr ? <Alert type="error" message={replayErr} /> : <Spin tip="Loading recordings…" />}
          </Modal>
        )
      ) : null}
    </ReplayContext.Provider>
  );
}

export function useReplay() {
  const ctx = useContext(ReplayContext);
  if (!ctx) throw new Error('useReplay must be used within ReplayProvider');
  return ctx;
}

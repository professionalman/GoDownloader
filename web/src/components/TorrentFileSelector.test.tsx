import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { TorrentFileSelector } from './TorrentFileSelector';
import * as api from '../api';

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return { ...actual, getTorrentFiles: vi.fn().mockResolvedValue([{ index: 0, path: 'a.bin', size: 10, progress: 0, priority: 'normal', selected: true }]) };
});

it('submits the normalized five-mode seeding policy', async () => {
  const onStart = vi.fn();
  render(<TorrentFileSelector job={{ id: 'j1', name: 'Torrent' } as never} onStart={onStart} onClose={vi.fn()} />);
  await waitFor(() => expect(api.getTorrentFiles).toHaveBeenCalled());
  fireEvent.change(screen.getByLabelText('Seeding policy'), { target: { value: 'ratio_or_duration' } });
  fireEvent.change(screen.getByLabelText('Ratio target'), { target: { value: '2.5' } });
  fireEvent.change(screen.getByLabelText('Active seeding hours'), { target: { value: '12' } });
  fireEvent.click(screen.getByRole('button', { name: 'Start Download' }));
  expect(onStart.mock.calls[0][2]).toEqual({ mode: 'ratio_or_duration', ratioLimit: 2.5, timeLimitSeconds: 43200 });
});

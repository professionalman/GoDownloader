import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { SubtitleSelector } from './SubtitleSelector';
import type { SubtitleCapabilities } from '../types';

describe('SubtitleSelector Component', () => {
  it('1 & 2. renders "No subtitles available" when no subtitles are available and hides format/output controls', () => {
    const onChange = vi.fn();
    render(<SubtitleSelector subtitles={undefined} onChange={onChange} />);

    expect(screen.getByText('No subtitles available')).toBeInTheDocument();
    expect(screen.queryByText('Format')).not.toBeInTheDocument();
    expect(screen.queryByText('Output')).not.toBeInTheDocument();
    expect(screen.queryByText('Add English translation')).not.toBeInTheDocument();
  });

  it('3. renders manual subtitle with Manual badge', () => {
    const caps: SubtitleCapabilities = {
      tracks: [
        { language: 'ja', name: 'Japanese', manual: true, auto: false, formats: ['vtt'] },
      ],
      englishTranslationAvailable: false,
    };
    const onChange = vi.fn();
    render(<SubtitleSelector subtitles={caps} onChange={onChange} />);

    expect(screen.getByText('Japanese (ja)')).toBeInTheDocument();
    expect(screen.getByText('Manual')).toBeInTheDocument();
  });

  it('4. renders auto-generated subtitle with Auto-generated badge', () => {
    const caps: SubtitleCapabilities = {
      tracks: [
        { language: 'fr', name: 'French', manual: false, auto: true, formats: ['vtt'] },
      ],
      englishTranslationAvailable: false,
    };
    const onChange = vi.fn();
    render(<SubtitleSelector subtitles={caps} onChange={onChange} />);

    expect(screen.getByText('French (fr)')).toBeInTheDocument();
    expect(screen.getByText('Auto-generated')).toBeInTheDocument();
  });

  it('5. renders single row for language with both manual and auto as "Manual · Auto available"', () => {
    const caps: SubtitleCapabilities = {
      tracks: [
        { language: 'en', name: 'English', manual: true, auto: true, formats: ['vtt'] },
      ],
      englishTranslationAvailable: false,
    };
    const onChange = vi.fn();
    render(<SubtitleSelector subtitles={caps} onChange={onChange} />);

    expect(screen.getByText('English (en)')).toBeInTheDocument();
    expect(screen.getByText('Manual · Auto available')).toBeInTheDocument();
    // Verify only one English checkbox rendered
    const checkboxes = screen.getAllByRole('checkbox');
    // 1 for English track + 1 for "Allow auto-generated subtitles"
    expect(checkboxes.length).toBe(2);
  });

  it('6, 7, 8. displays multiple languages, selects multiple, and reveals format/output controls', () => {
    const caps: SubtitleCapabilities = {
      tracks: [
        { language: 'ja', name: 'Japanese', manual: true, auto: false },
        { language: 'es', name: 'Spanish', manual: true, auto: false },
        { language: 'de', name: 'German', manual: true, auto: false },
      ],
      englishTranslationAvailable: false,
    };
    const onChange = vi.fn();
    render(<SubtitleSelector subtitles={caps} onChange={onChange} />);

    // Initially format and output controls are not rendered
    expect(screen.queryByText('Format')).not.toBeInTheDocument();
    expect(screen.queryByText('Output')).not.toBeInTheDocument();

    // Select Japanese
    const jaCheckbox = screen.getByLabelText(/Japanese/i);
    fireEvent.click(jaCheckbox);

    // Format and Output controls are now revealed
    expect(screen.getByText('Format')).toBeInTheDocument();
    expect(screen.getByText('Output')).toBeInTheDocument();
    expect(onChange).toHaveBeenLastCalledWith({
      languages: ['ja'],
      includeAuto: false,
      englishTranslation: false,
      mode: 'separate',
      format: 'srt',
    });

    // Select Spanish as well
    const esCheckbox = screen.getByLabelText(/Spanish/i);
    fireEvent.click(esCheckbox);

    expect(onChange).toHaveBeenLastCalledWith({
      languages: ['ja', 'es'],
      includeAuto: false,
      englishTranslation: false,
      mode: 'separate',
      format: 'srt',
    });
  });

  it('9. toggles "Allow auto-generated subtitles"', () => {
    const caps: SubtitleCapabilities = {
      tracks: [
        { language: 'fr', name: 'French', manual: false, auto: true },
      ],
      englishTranslationAvailable: false,
    };
    const onChange = vi.fn();
    render(<SubtitleSelector subtitles={caps} onChange={onChange} />);

    // Select French
    fireEvent.click(screen.getByLabelText(/French/i));

    // Enable auto-generated subtitles
    const autoToggle = screen.getByLabelText('Allow auto-generated subtitles');
    fireEvent.click(autoToggle);

    expect(onChange).toHaveBeenLastCalledWith({
      languages: ['fr'],
      includeAuto: true,
      englishTranslation: false,
      mode: 'separate',
      format: 'srt',
    });
  });

  it('10 & 23. clearing selected languages resets options and sends undefined', () => {
    const caps: SubtitleCapabilities = {
      tracks: [
        { language: 'ja', name: 'Japanese', manual: true, auto: false },
      ],
      englishTranslationAvailable: false,
    };
    const onChange = vi.fn();
    render(<SubtitleSelector subtitles={caps} onChange={onChange} />);

    // Select Japanese
    const jaCheckbox = screen.getByLabelText(/Japanese/i);
    fireEvent.click(jaCheckbox);
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ languages: ['ja'] }));

    // Click Clear button
    const clearBtn = screen.getByText('Clear subtitle selection');
    fireEvent.click(clearBtn);

    expect(onChange).toHaveBeenLastCalledWith(undefined);
    expect(screen.queryByText('Format')).not.toBeInTheDocument();
  });

  it('11 & 12. shows English translation checkbox only when available', () => {
    const capsUnavailable: SubtitleCapabilities = {
      tracks: [{ language: 'es', name: 'Spanish', manual: true, auto: false }],
      englishTranslationAvailable: false,
    };
    const { rerender } = render(<SubtitleSelector subtitles={capsUnavailable} onChange={vi.fn()} />);
    expect(screen.queryByText('Add English translation')).not.toBeInTheDocument();

    const capsAvailable: SubtitleCapabilities = {
      tracks: [{ language: 'ja', name: 'Japanese', manual: true, auto: false }],
      englishTranslationAvailable: true,
    };
    rerender(<SubtitleSelector subtitles={capsAvailable} onChange={vi.fn()} />);
    expect(screen.getByText('Add English translation')).toBeInTheDocument();
  });

  it('13 & 14. native English subtitle is shown normally and not labelled as translation', () => {
    const caps: SubtitleCapabilities = {
      tracks: [
        { language: 'en', name: 'English', manual: true, auto: false },
      ],
      englishTranslationAvailable: false,
    };
    render(<SubtitleSelector subtitles={caps} onChange={vi.fn()} />);

    expect(screen.getByText('English (en)')).toBeInTheDocument();
    expect(screen.getByText('Manual')).toBeInTheDocument();
    expect(screen.queryByText('Add English translation')).not.toBeInTheDocument();
  });

  it('15 & 16. selecting English translation alone and with normal subtitle', () => {
    const caps: SubtitleCapabilities = {
      tracks: [
        { language: 'ja', name: 'Japanese', manual: true, auto: false },
      ],
      englishTranslationAvailable: true,
    };
    const onChange = vi.fn();
    render(<SubtitleSelector subtitles={caps} onChange={onChange} />);

    // Select only English translation
    const transCheckbox = screen.getByLabelText('Add English translation');
    fireEvent.click(transCheckbox);

    expect(onChange).toHaveBeenLastCalledWith({
      languages: [],
      includeAuto: false,
      englishTranslation: true,
      mode: 'separate',
      format: 'srt',
    });

    // Also select Japanese
    fireEvent.click(screen.getByLabelText(/Japanese/i));

    expect(onChange).toHaveBeenLastCalledWith({
      languages: ['ja'],
      includeAuto: false,
      englishTranslation: true,
      mode: 'separate',
      format: 'srt',
    });
  });

  it('17 & 18. format radio selection (SRT vs VTT)', () => {
    const caps: SubtitleCapabilities = {
      tracks: [{ language: 'ja', name: 'Japanese', manual: true, auto: false }],
      englishTranslationAvailable: false,
    };
    const onChange = vi.fn();
    render(<SubtitleSelector subtitles={caps} onChange={onChange} />);

    fireEvent.click(screen.getByLabelText(/Japanese/i));

    // Default is SRT
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ format: 'srt' }));

    // Select VTT
    fireEvent.click(screen.getByLabelText('VTT'));
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ format: 'vtt' }));

    // Select SRT back
    fireEvent.click(screen.getByLabelText('SRT'));
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ format: 'srt' }));
  });

  it('19, 20, 21. output mode radio selection (Separate vs Embed vs Both)', () => {
    const caps: SubtitleCapabilities = {
      tracks: [{ language: 'ja', name: 'Japanese', manual: true, auto: false }],
      englishTranslationAvailable: false,
    };
    const onChange = vi.fn();
    render(<SubtitleSelector subtitles={caps} onChange={onChange} />);

    fireEvent.click(screen.getByLabelText(/Japanese/i));

    // Default is Separate
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ mode: 'separate' }));

    // Select Embed
    fireEvent.click(screen.getByLabelText('Embed'));
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ mode: 'embed' }));

    // Select Both
    fireEvent.click(screen.getByLabelText('Both'));
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ mode: 'both' }));
  });
});

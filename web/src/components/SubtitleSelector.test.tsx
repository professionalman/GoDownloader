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

  it('3 & 13. renders manual subtitle with Manual badge visible by default', () => {
    const caps: SubtitleCapabilities = {
      tracks: [
        { language: 'ja', name: 'Japanese', manual: true, auto: false, formats: ['vtt'] },
      ],
      englishTranslationAvailable: false,
    };
    const onChange = vi.fn();
    render(<SubtitleSelector subtitles={caps} onChange={onChange} />);

    expect(screen.getByText('Available subtitles')).toBeInTheDocument();
    expect(screen.getByText('Japanese (ja)')).toBeInTheDocument();
    expect(screen.getByText('Manual')).toBeInTheDocument();
    expect(screen.queryByText('Auto-generated subtitles')).not.toBeInTheDocument();
  });

  it('4, 14, 15. auto-only tracks hidden by default and appear under Auto-generated subtitles when toggle ON', () => {
    const caps: SubtitleCapabilities = {
      tracks: [
        { language: 'fr', name: 'French', manual: false, auto: true, formats: ['vtt'] },
      ],
      englishTranslationAvailable: false,
    };
    const onChange = vi.fn();
    render(<SubtitleSelector subtitles={caps} onChange={onChange} />);

    // By default: toggle is present, but French (auto-only) is hidden
    expect(screen.queryByText('Available subtitles')).not.toBeInTheDocument();
    expect(screen.queryByText('French (fr)')).not.toBeInTheDocument();
    expect(screen.queryByText('Auto-generated subtitles')).not.toBeInTheDocument();

    const autoToggle = screen.getByLabelText('Allow auto-generated subtitles');
    expect(autoToggle).toBeInTheDocument();

    // Turn toggle ON
    fireEvent.click(autoToggle);

    // Auto-generated subtitles section is now revealed
    expect(screen.getByText('Auto-generated subtitles')).toBeInTheDocument();
    expect(screen.getByText('French (fr)')).toBeInTheDocument();
    expect(screen.getByText('Auto-generated')).toBeInTheDocument();
  });

  it('5 & 18. renders single row for language with both manual and auto as "Manual · Auto available" in Available subtitles', () => {
    const caps: SubtitleCapabilities = {
      tracks: [
        { language: 'en', name: 'English', manual: true, auto: true, formats: ['vtt'] },
      ],
      englishTranslationAvailable: false,
    };
    const onChange = vi.fn();
    render(<SubtitleSelector subtitles={caps} onChange={onChange} />);

    expect(screen.getByText('Available subtitles')).toBeInTheDocument();
    expect(screen.getByText('English (en)')).toBeInTheDocument();
    expect(screen.getByText('Manual · Auto available')).toBeInTheDocument();
    // Auto toggle is also present because auto exists
    expect(screen.getByLabelText('Allow auto-generated subtitles')).toBeInTheDocument();
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

  it('9 & 15. selecting an auto-only track when toggle ON sends includeAuto: true', () => {
    const caps: SubtitleCapabilities = {
      tracks: [
        { language: 'pa-orig', name: 'Punjabi (Original)', manual: false, auto: true },
      ],
      englishTranslationAvailable: false,
    };
    const onChange = vi.fn();
    render(<SubtitleSelector subtitles={caps} onChange={onChange} />);

    // Enable auto toggle to reveal the track
    const autoToggle = screen.getByLabelText('Allow auto-generated subtitles');
    fireEvent.click(autoToggle);

    // Select Punjabi
    fireEvent.click(screen.getByLabelText(/Punjabi/i));

    expect(onChange).toHaveBeenLastCalledWith({
      languages: ['pa-orig'],
      includeAuto: true,
      englishTranslation: false,
      mode: 'separate',
      format: 'srt',
    });
  });

  it('16 & 17. turning toggle OFF clears selected auto-only tracks while preserving manual selections', () => {
    const caps: SubtitleCapabilities = {
      tracks: [
        { language: 'en', name: 'English', manual: true, auto: false },
        { language: 'pa-orig', name: 'Punjabi (Original)', manual: false, auto: true },
      ],
      englishTranslationAvailable: false,
    };
    const onChange = vi.fn();
    render(<SubtitleSelector subtitles={caps} onChange={onChange} />);

    // Select manual English
    fireEvent.click(screen.getByLabelText(/English/i));
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({
      languages: ['en'],
      includeAuto: false,
    }));

    // Turn auto toggle ON and select Punjabi
    const autoToggle = screen.getByLabelText('Allow auto-generated subtitles');
    fireEvent.click(autoToggle);
    fireEvent.click(screen.getByLabelText(/Punjabi/i));
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({
      languages: ['en', 'pa-orig'],
      includeAuto: true,
    }));

    // Turn auto toggle OFF -> Punjabi should be cleared, English remains
    fireEvent.click(autoToggle);
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({
      languages: ['en'],
      includeAuto: false,
    }));
    expect(screen.queryByText('Punjabi (Original) (pa-orig)')).not.toBeInTheDocument();
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

  it('11, 12, 19. English translation option remains independent and is shown only when available', () => {
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
    const onChange = vi.fn();
    rerender(<SubtitleSelector subtitles={capsAvailable} onChange={onChange} />);
    expect(screen.getByText('Add English translation')).toBeInTheDocument();

    // Select English translation independently
    fireEvent.click(screen.getByLabelText('Add English translation'));
    expect(onChange).toHaveBeenLastCalledWith({
      languages: [],
      includeAuto: false,
      englishTranslation: true,
      mode: 'separate',
      format: 'srt',
    });
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

  it('17 & 18 & 20. format radio selection (SRT vs VTT)', () => {
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

  it('19, 20. output mode radio selection (Separate vs Embed vs Both)', () => {
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

  it('21. no auto tracks + manual tracks still renders correctly without auto-generated list', () => {
    const caps: SubtitleCapabilities = {
      tracks: [
        { language: 'es', name: 'Spanish', manual: true, auto: false },
        { language: 'de', name: 'German', manual: true, auto: false },
      ],
      englishTranslationAvailable: false,
    };
    render(<SubtitleSelector subtitles={caps} onChange={vi.fn()} />);

    expect(screen.getByText('Available subtitles')).toBeInTheDocument();
    expect(screen.getByText('Spanish (es)')).toBeInTheDocument();
    expect(screen.getByText('German (de)')).toBeInTheDocument();
    expect(screen.queryByText('Auto-generated subtitles')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Allow auto-generated subtitles')).not.toBeInTheDocument();
  });

  it('22. no manual tracks + auto tracks shows toggle and reveals tracks only after toggle ON', () => {
    const caps: SubtitleCapabilities = {
      tracks: [
        { language: 'pa-orig', name: 'Punjabi (Original)', manual: false, auto: true },
      ],
      englishTranslationAvailable: true,
    };
    render(<SubtitleSelector subtitles={caps} onChange={vi.fn()} />);

    // No available subtitles section
    expect(screen.queryByText('Available subtitles')).not.toBeInTheDocument();
    expect(screen.queryByText('Punjabi (Original) (pa-orig)')).not.toBeInTheDocument();

    // Toggle and English translation are shown
    const autoToggle = screen.getByLabelText('Allow auto-generated subtitles');
    expect(autoToggle).toBeInTheDocument();
    expect(screen.getByText('Add English translation')).toBeInTheDocument();

    // Check toggle
    fireEvent.click(autoToggle);

    // Auto-generated section is revealed
    expect(screen.getByText('Auto-generated subtitles')).toBeInTheDocument();
    expect(screen.getByText('Punjabi (Original) (pa-orig)')).toBeInTheDocument();
  });
});

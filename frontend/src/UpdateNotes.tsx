import type { ReactNode } from 'react';

type SectionTone = 'important' | 'added' | 'fixed' | 'improved' | 'general';

export type UpdateNoteSection = {
  title: string;
  tone: SectionTone;
  priority: number;
  items: string[];
};

export type ParsedUpdateNotes = {
  headline?: string;
  sections: UpdateNoteSection[];
};

const categoryMeta: Record<string, { title: string; tone: SectionTone; priority: number }> = {
  重要变更: { title: '重要变更', tone: 'important', priority: 0 },
  破坏性变更: { title: '重要变更', tone: 'important', priority: 0 },
  升级说明: { title: '重要变更', tone: 'important', priority: 0 },
  新增: { title: '新增', tone: 'added', priority: 1 },
  新功能: { title: '新增', tone: 'added', priority: 1 },
  修复: { title: '修复', tone: 'fixed', priority: 2 },
  问题修复: { title: '修复', tone: 'fixed', priority: 2 },
  优化: { title: '优化', tone: 'improved', priority: 3 },
  改进: { title: '优化', tone: 'improved', priority: 3 },
  体验优化: { title: '优化', tone: 'improved', priority: 3 },
};

function matchHeading(line: string): { level: 1 | 2; text: string } | undefined {
  const h1 = line.match(/^#\s+(.+)$/);
  if (h1) return { level: 1, text: h1[1].trim() };
  const h2 = line.match(/^#{2,3}\s+(.+)$/);
  if (h2) return { level: 2, text: h2[1].trim() };
  const bracket = line.match(/^【([^】]+)】\s*$/);
  if (bracket) return { level: 2, text: bracket[1].trim() };
  return undefined;
}

export function parseUpdateNotes(raw: string): ParsedUpdateNotes {
  const lines = raw.replace(/\r\n/g, '\n').split('\n');
  let headline: string | undefined;
  const sections: UpdateNoteSection[] = [];
  let current: UpdateNoteSection | undefined;

  const ensureSection = (rawTitle: string): UpdateNoteSection => {
    const clean = rawTitle.replace(/^【|】$/g, '').trim();
    const meta = categoryMeta[clean];
    const title = meta?.title ?? clean;
    const existing = sections.find((s) => s.title === title);
    if (existing) return existing;
    const created: UpdateNoteSection = {
      title,
      tone: meta?.tone ?? 'general',
      priority: meta?.priority ?? (title ? 4 : 5),
      items: [],
    };
    sections.push(created);
    return created;
  };

  for (const rawLine of lines) {
    const line = rawLine.trim();
    if (!line) continue;
    // 兼容旧版 squash 提交说明，自动忽略分隔线与协作者脚注。
    if (/^-{3,}$/.test(line) || /^Co-authored-by:/i.test(line)) continue;

    const heading = matchHeading(line);
    if (heading) {
      if (heading.level === 1 && !headline && sections.length === 0) {
        headline = heading.text;
      } else {
        current = ensureSection(heading.text);
      }
      continue;
    }

    const bullet = line.match(/^[-*•]\s+(.+)$/);
    if (bullet) {
      current ??= ensureSection('');
      current.items.push(bullet[1].trim());
      continue;
    }

    if (!headline && sections.length === 0 && !current) {
      headline = line;
      continue;
    }

    current ??= ensureSection('');
    current.items.push(line);
  }

  const nonEmpty = sections
    .filter((s) => s.items.length > 0)
    .sort((a, b) => a.priority - b.priority);

  return { headline, sections: nonEmpty };
}

function renderInline(text: string): ReactNode[] {
  const parts: ReactNode[] = [];
  const pattern = /(\*\*[^*]+\*\*|`[^`]+`)/g;
  let lastIndex = 0;
  let match: RegExpExecArray | null;
  let key = 0;

  while ((match = pattern.exec(text)) !== null) {
    if (match.index > lastIndex) {
      parts.push(text.slice(lastIndex, match.index));
    }
    const token = match[0];
    if (token.startsWith('**') && token.endsWith('**')) {
      parts.push(
        <strong key={key++} className="update-note-emphasis">
          {token.slice(2, -2)}
        </strong>,
      );
    } else if (token.startsWith('`') && token.endsWith('`')) {
      parts.push(
        <code key={key++} className="update-note-code mono">
          {token.slice(1, -1)}
        </code>,
      );
    }
    lastIndex = pattern.lastIndex;
  }

  if (lastIndex < text.length) {
    parts.push(text.slice(lastIndex));
  }
  return parts;
}

export function UpdateNotes({
  version,
  notes,
  variant = 'card',
}: {
  version: string;
  notes: string;
  variant?: 'card' | 'dialog';
}) {
  const parsed = parseUpdateNotes(notes || '');
  const empty = !parsed.headline && parsed.sections.length === 0;

  return (
    <div className={`release-notes release-notes-${variant}`}>
      {(variant === 'card' || parsed.headline) && (
        <div className="release-notes-head">
          {variant === 'card' && (
            <span className="release-notes-ver mono">v{version}</span>
          )}
          <span className="release-notes-headline">
            {parsed.headline || (variant === 'card' ? '本次更新内容' : '')}
          </span>
        </div>
      )}

      {empty ? (
        <p className="release-notes-empty">本次更新未提供说明。</p>
      ) : (
        <div className="release-notes-groups">
          {parsed.sections.map((section, idx) => (
            <div key={section.title || `group-${idx}`} className="release-notes-group">
              {section.title && (
                <div className="release-notes-group-head">
                  <span className={`release-notes-tag tone-${section.tone}`}>
                    {section.title}
                  </span>
                </div>
              )}
              <ul className="release-notes-list">
                {section.items.map((item, itemIdx) => (
                  <li key={itemIdx} className="release-notes-item">
                    {renderInline(item)}
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

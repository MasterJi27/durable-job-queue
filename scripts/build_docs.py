#!/usr/bin/env python3
"""Render the Markdown handbook with ReportLab; reject unexpected page spills.

Supported subset: headings, paragraphs, lists, tables, code fences, callouts,
page markers, and the vector architecture illustration. Run from any directory.
"""
from __future__ import annotations

import html
import re
from pathlib import Path

from reportlab.lib import colors
from reportlab.lib.pagesizes import A4
from reportlab.lib.styles import ParagraphStyle
from reportlab.pdfbase.pdfmetrics import registerFont, stringWidth
from reportlab.pdfbase.ttfonts import TTFont
from reportlab.platypus import (
    BaseDocTemplate, Flowable, Frame, KeepTogether, PageBreak, PageTemplate,
    Paragraph, Preformatted, Spacer, Table, TableStyle,
)

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / 'docs' / 'DEVELOPER_GUIDE.md'
OUTPUT = ROOT / 'docs' / 'DEVELOPER_GUIDE.pdf'
PAGE_W, PAGE_H = A4
MARGIN = 48
WIDTH = PAGE_W - 2 * MARGIN
INK = colors.HexColor('#172438')
MUTED = colors.HexColor('#536275')
ACCENT = colors.HexColor('#cf543b')
PALE = colors.HexColor('#f2f5f8')
LINE = colors.HexColor('#d8e0e8')

# Embed common metric-compatible fonts when available, so the reference PDF
# renders consistently. Standard PDF fonts remain a no-extra-dependency fallback.
FONT_SETS = [
    (Path('/System/Library/Fonts/Supplemental'),
        ('Arial.ttf', 'Arial Bold.ttf', 'Arial Italic.ttf',
         'Arial Bold Italic.ttf', 'Courier New.ttf')),
    (Path('/usr/share/fonts/truetype/liberation2'),
        ('LiberationSans-Regular.ttf', 'LiberationSans-Bold.ttf',
         'LiberationSans-Italic.ttf', 'LiberationSans-BoldItalic.ttf',
         'LiberationMono-Regular.ttf')),
    (Path('C:/Windows/Fonts'),
        ('arial.ttf', 'arialbd.ttf', 'ariali.ttf', 'arialbi.ttf', 'cour.ttf')),
]
for font_dir, filenames in FONT_SETS:
    if all((font_dir / name).is_file() for name in filenames):
        for alias, filename in zip(
                ('Helvetica', 'Helvetica-Bold', 'Helvetica-Oblique',
                 'Helvetica-BoldOblique', 'Courier'), filenames):
            registerFont(TTFont(alias, str(font_dir / filename)))
        break

ST = {
    'body': ParagraphStyle('Body', fontName='Helvetica', fontSize=10,
        leading=14, textColor=INK, spaceAfter=8),
    'section': ParagraphStyle('Section', fontName='Helvetica-Bold', fontSize=22,
        leading=27, textColor=INK, spaceAfter=17, keepWithNext=True),
    'sub': ParagraphStyle('Sub', fontName='Helvetica-Bold', fontSize=12,
        leading=16, textColor=INK, spaceBefore=7, spaceAfter=7, keepWithNext=True),
    'cell': ParagraphStyle('Cell', fontName='Helvetica', fontSize=9.1,
        leading=12.3, textColor=INK),
    'th': ParagraphStyle('TH', fontName='Helvetica-Bold', fontSize=9,
        leading=12, textColor=colors.white),
    'quote': ParagraphStyle('Quote', fontName='Helvetica', fontSize=10.5,
        leading=15.5, textColor=INK),
    'code': ParagraphStyle('Code', fontName='Courier', fontSize=9,
        leading=12.2, textColor=INK),
    'tiny': ParagraphStyle('Tiny', fontName='Helvetica', fontSize=8,
        leading=11, textColor=MUTED),
}


def inline(raw):
    text = html.escape(raw)
    text = re.sub(r'`([^`]+)`', r'<font name="Courier" size="9">\1</font>', text)
    text = re.sub(r'\*\*([^*]+)\*\*', r'<b>\1</b>', text)
    return re.sub(r'\[([^]]+)\]\(([^)]+)\)',
        r'<link href="\2" color="#b34631">\1</link>', text)


class StartSection(Flowable):
    def __init__(self, index, title):
        super().__init__()
        self.index, self.title = index, title
        self.width = self.height = 0

    def draw(self):
        self.canv.bookmarkPage(f'p{self.index}')
        self.canv.addOutlineEntry(self.title, f'p{self.index}', level=0)


class Architecture(Flowable):
    def __init__(self):
        super().__init__()
        self.width, self.height = WIDTH, 142

    def draw(self):
        c = self.canv
        w, gap = (WIDTH - 36) / 3, 18

        def box(x, y, title, note, dark=False):
            c.setFillColor(INK if dark else PALE)
            c.setStrokeColor(INK if dark else LINE)
            c.roundRect(x, y, w, 52, 6, stroke=1, fill=1)
            c.setFillColor(colors.white if dark else INK)
            c.setFont('Helvetica-Bold', 10)
            c.drawCentredString(x + w / 2, y + 31, title)
            c.setFont('Helvetica', 8.2)
            c.drawCentredString(x + w / 2, y + 15, note)

        def arrow(x1, y, x2):
            c.setStrokeColor(ACCENT)
            c.setLineWidth(1.5)
            c.line(x1, y, x2, y)
            direction = 1 if x2 > x1 else -1
            c.line(x2, y, x2 - direction * 4, y + 3)
            c.line(x2, y, x2 - direction * 4, y - 3)

        x2, x3 = w + gap, 2 * (w + gap)
        box(0, 80, 'CLI / HTTP + GUI', 'Submit and inspect jobs')
        box(x2, 80, 'PostgreSQL', 'Jobs + leases + unique results', True)
        box(x3, 80, 'Go workers', 'Claim, renew, process, commit')
        arrow(w, 106, x2)
        arrow(x3, 106, x2 + w)
        box(0, 9, 'Seeded simulator', 'Crashes, duplicates, clock jumps')
        box(x2, 9, 'Memory store', 'Reference clock + shared rules')
        box(x3, 9, 'Assertions', 'Accounting, fencing, replay')
        arrow(w, 35, x2)
        arrow(x2 + w, 35, x3)


def panel(flowable, quote=False):
    table = Table([[flowable]], colWidths=[WIDTH])
    commands = [
        ('BACKGROUND', (0, 0), (-1, -1), colors.HexColor('#fcf1ed') if quote else PALE),
        ('LEFTPADDING', (0, 0), (-1, -1), 13 if quote else 11),
        ('RIGHTPADDING', (0, 0), (-1, -1), 13 if quote else 11),
        ('TOPPADDING', (0, 0), (-1, -1), 10),
        ('BOTTOMPADDING', (0, 0), (-1, -1), 10),
    ]
    commands.append(('LINEBEFORE' if quote else 'BOX', (0, 0), (-1, -1),
        3 if quote else .5, ACCENT if quote else LINE))
    table.setStyle(TableStyle(commands))
    return [KeepTogether([table]), Spacer(1, 10)]


def block_table(rows, toc=False):
    widths = ([34, WIDTH / 2 - 34, 34, WIDTH / 2 - 34]
        if len(rows[0]) == 4 else [WIDTH * .36, WIDTH * .64])
    values = []
    for i, row in enumerate(rows):
        cells = []
        for value in row:
            content = inline(value)
            if toc and i and value.isdigit():
                content = f'<link href="#p{value}" color="#b34631">{value}</link>'
            cells.append(Paragraph(content, ST['th' if i == 0 else 'cell']))
        values.append(cells)
    table = Table(values, colWidths=widths, repeatRows=1, hAlign='LEFT')
    table.setStyle(TableStyle([
        ('BACKGROUND', (0, 0), (-1, 0), INK),
        ('ROWBACKGROUNDS', (0, 1), (-1, -1), [colors.white, PALE]),
        ('VALIGN', (0, 0), (-1, -1), 'TOP'),
        ('LEFTPADDING', (0, 0), (-1, -1), 9),
        ('RIGHTPADDING', (0, 0), (-1, -1), 9),
        ('TOPPADDING', (0, 0), (-1, -1), 5),
        ('BOTTOMPADDING', (0, 0), (-1, -1), 5),
        ('LINEBELOW', (0, -1), (-1, -1), .5, LINE),
    ]))
    return [table, Spacer(1, 11)]


def parse(section, index):
    lines = section.strip().splitlines()
    out, i = [], 0
    while i < len(lines):
        line = lines[i].strip()
        if not line:
            i += 1
        elif line.startswith('<!-- diagram:'):
            out += [Architecture(), Spacer(1, 10)]
            i += 1
        elif line.startswith('```'):
            code = []
            i += 1
            while i < len(lines) and not lines[i].startswith('```'):
                code.append(lines[i].rstrip())
                i += 1
            for row in code:
                if stringWidth(row, 'Courier', ST['code'].fontSize) > WIDTH - 22:
                    raise ValueError(f'Overwide code on planned page {index}: {row}')
            out += panel(Preformatted('\n'.join(code), ST['code']))
            i += 1
        elif line.startswith('|'):
            rows = []
            while i < len(lines) and lines[i].strip().startswith('|'):
                row = [s.strip() for s in lines[i].strip().strip('|').split('|')]
                if not all(re.fullmatch(r'[-: ]+', cell) for cell in row):
                    rows.append(row)
                i += 1
            out += block_table(rows, toc=index == 2)
        elif line.startswith('### '):
            out.append(Paragraph(inline(line[4:]), ST['sub']))
            i += 1
        elif line.startswith('## '):
            out.append(Paragraph(inline(line[3:]), ST['section']))
            i += 1
        elif line.startswith('> '):
            text = []
            while i < len(lines) and lines[i].strip().startswith('> '):
                text.append(lines[i].strip()[2:])
                i += 1
            out += panel(Paragraph(inline(' '.join(text)), ST['quote']), quote=True)
        elif line.startswith('- ') or re.match(r'\d+\. ', line):
            bullet, text = line.split(' ', 1)
            style = ParagraphStyle('List', parent=ST['body'], leftIndent=14,
                bulletIndent=0, spaceAfter=5)
            out.append(Paragraph(inline(text), style, bulletText=bullet))
            i += 1
        else:
            paragraph = [line]
            i += 1
            while i < len(lines) and lines[i].strip() and not lines[i].startswith(
                    ('#', '|', '```', '> ', '- ', '<!--')):
                paragraph.append(lines[i].strip())
                i += 1
            out.append(Paragraph(inline(' '.join(paragraph)), ST['body']))
    return out


def cover(section):
    remainder = section[section.index('## Reliable'):]
    title = ParagraphStyle('CoverTitle', fontName='Helvetica-Bold', fontSize=40,
        leading=44, textColor=INK, spaceAfter=16)
    subtitle = ParagraphStyle('CoverSubtitle', fontName='Helvetica', fontSize=21,
        leading=27, textColor=MUTED, spaceAfter=18)
    label = ParagraphStyle('Label', fontName='Helvetica-Bold', fontSize=10,
        leading=14, textColor=ACCENT, spaceAfter=18)
    # A smaller cover subheading leaves room for the reader's first overview.
    remainder = remainder.replace('## Reliable', '### Reliable', 1)
    return [Spacer(1, 20),
        Paragraph('DURABLE. / ENGINEERING DOCUMENTATION', label),
        Paragraph('Durable<br/>Job Queue', title),
        Paragraph('Developer Handbook', subtitle),
        Paragraph('Go + PostgreSQL + deterministic simulation', ST['body']),
        Paragraph('Documentation snapshot / 18 October 2026', ST['tiny']),
        Spacer(1, 18), *parse(remainder, 1)]


class Handbook(BaseDocTemplate):
    def __init__(self, path, titles):
        super().__init__(str(path), pagesize=A4, leftMargin=MARGIN,
            rightMargin=MARGIN, topMargin=66, bottomMargin=48,
            title='Durable Job Queue - Developer Handbook',
            author='Durable Job Queue project', subject='Implementation and contributor guide')
        self.titles, self.starts = titles, []
        frame = Frame(MARGIN, 48, WIDTH, PAGE_H - 114,
            leftPadding=0, rightPadding=0, topPadding=0, bottomPadding=0)
        self.addPageTemplates([PageTemplate(id='handbook', frames=[frame],
            onPage=self.decorate)])

    def decorate(self, canvas, doc):
        canvas.saveState()
        canvas.setFillColor(ACCENT)
        canvas.rect(MARGIN, PAGE_H - 35, 25, 3, fill=1, stroke=0)
        if doc.page > 1:
            canvas.setFillColor(MUTED)
            canvas.setFont('Helvetica', 8)
            canvas.drawString(MARGIN + 34, PAGE_H - 34,
                'DURABLE JOB QUEUE / DEVELOPER HANDBOOK')
        canvas.setStrokeColor(LINE)
        canvas.setLineWidth(.5)
        canvas.line(MARGIN, 34, PAGE_W - MARGIN, 34)
        canvas.setFillColor(MUTED)
        canvas.setFont('Helvetica', 8)
        canvas.drawString(MARGIN, 21, 'Project documentation | 18 October 2026')
        canvas.drawRightString(PAGE_W - MARGIN, 21,
            f'{doc.page:02d} / {len(self.titles):02d}')
        canvas.restoreState()

    def afterFlowable(self, item):
        if isinstance(item, StartSection):
            self.starts.append((item.index, self.page, item.title))


def main():
    source = SOURCE.read_text(encoding='utf-8')
    if any(ord(c) > 127 for c in source):
        raise ValueError('Use ASCII source for font and copy/paste portability')
    sections = source.split('<!-- page -->')
    titles = ['Cover'] + [re.search(r'^## (.+)$', s, re.M).group(1)
        for s in sections[1:]]
    story = []
    for index, section in enumerate(sections, 1):
        if index > 1:
            story.append(PageBreak())
        story.append(StartSection(index, titles[index - 1]))
        story.extend(cover(section) if index == 1 else parse(section, index))
    candidate = OUTPUT.with_suffix('.candidate.pdf')
    doc = Handbook(candidate, titles)
    doc.build(story)
    mismatches = [(e, a, title) for e, a, title in doc.starts if e != a]
    if mismatches or doc.page != len(sections):
        raise ValueError(f'Unexpected page spills: {mismatches}; total={doc.page}; '
            f'inspect {candidate}')
    candidate.replace(OUTPUT)
    print(f'Created {OUTPUT}: {doc.page} pages, {OUTPUT.stat().st_size:,} bytes')


if __name__ == '__main__':
    main()

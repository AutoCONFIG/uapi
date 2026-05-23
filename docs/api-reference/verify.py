#!/usr/bin/env python3
from pathlib import Path
import hashlib,re,sys
root=Path(__file__).resolve().parent; errors=[]
def fail(x): errors.append(x)
def count(path,pat): return len([p for p in (root/path).rglob(pat) if p.is_file() and p.name!='INDEX.md'])
if {p.name for p in root.iterdir() if p.is_dir()} != {'openai-chat-completions','openai-responses','gemini-api','anthropic-messages'}: fail('category directories mismatch')
for p in root.rglob('*'):
    if p.is_file() and p.stat().st_size==0: fail(f'zero-byte file: {p.relative_to(root)}')
openai=set(re.findall(r'https://developers\.openai\.com/api/docs/[^)\s]+\.md',(root/'openai-chat-completions/official/llms.txt').read_text()))
for c in ['openai-chat-completions','openai-responses']:
    n=count(Path(c)/'docs','*.md')
    if n!=len(openai): fail(f'{c} docs {n}!={len(openai)}')
if count(Path('openai-chat-completions')/'endpoints','*.yml')!=3: fail('OpenAI Chat endpoint count mismatch')
if count(Path('openai-responses')/'endpoints','*.yml')!=6: fail('OpenAI Responses endpoint count mismatch')
gurls=[x for x in (root/'gemini-api/official/urls.txt').read_text().splitlines() if x]
if len(gurls)!=168: fail(f'Gemini URL count {len(gurls)}!=168')
if count(Path('gemini-api')/'docs','*.md')!=168: fail('Gemini docs count mismatch')
if count(Path('gemini-api')/'raw','*.md.txt')!=168: fail('Gemini raw count mismatch')
aurls=[x for x in (root/'anthropic-messages/official/indexed-urls.txt').read_text().splitlines() if x]
amd=count(Path('anthropic-messages')/'docs','*.md'); ahtml=count(Path('anthropic-messages')/'html-fallback','*.html')
unavail=root/'anthropic-messages/official/unavailable-indexed-urls.txt'; au=len(unavail.read_text().splitlines()) if unavail.exists() else 0
if amd+ahtml+au!=len(aurls): fail(f'Anthropic represented {amd+ahtml}+unavailable {au}!={len(aurls)}')
for line in (root/'CHECKSUMS.sha256').read_text().splitlines():
    digest,rel=line.split('  ',1); p=root/rel.removeprefix('./')
    if not p.exists(): fail(f'missing checksum path {rel}')
    elif hashlib.sha256(p.read_bytes()).hexdigest()!=digest: fail(f'checksum mismatch {rel}')
if errors:
    print('\n'.join('ERROR: '+e for e in errors),file=sys.stderr); sys.exit(1)
print('API reference verification passed.')
print(f'OpenAI docs per category: {len(openai)}')
print('OpenAI endpoints: chat=3, responses=6')
print('Gemini docs/raw: 168/168')
print(f'Anthropic represented: {amd+ahtml}/{len(aurls)}; unavailable={au}')

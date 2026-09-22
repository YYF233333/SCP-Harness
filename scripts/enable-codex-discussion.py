#!/usr/bin/python3
"""Add discussion to the existing external /opt/scp-workers Codex bundle.

Run as root in SCP-Worker, with the normal scheduler stopped. Credentials,
runtime-start, volatile HOME and the OS boundary are left to the existing bundle.
"""
import json
from pathlib import Path

root = Path('/opt/scp-workers/codex-v0')
worker = root / 'codex-worker'
source = worker.read_text()
old = 'OPERATIONS = {"mutation", "review", "option_generation", "merge_judge", "merge_synth"}'
new = 'OPERATIONS = {"mutation", "review", "option_generation", "merge_judge", "merge_synth", "discussion"}'
if source.count(old) != 1 and source.count(new) != 1:
    raise RuntimeError('Unrecognized worker operation registry; no files changed')
if '"--dangerously-bypass-approvals-and-sandbox"' not in source:
    raise RuntimeError('Existing worker does not use the accepted execution boundary')
bindings_path = root / 'bindings.json'
bindings = json.loads(bindings_path.read_text())
binding = dict(bindings['operations']['option_generation'])
binding.update(role='discussion', prompt='prompts/discussion.md')
bindings['operations']['discussion'] = binding
prompt = '''# Bounded Option discussion

Read SCP_INPUT and the available SCP_CONTEXT files, including
discussion-instruction.txt, option.target.json, option.lineage.direct.json and
claim.related.json. Claims are ordered by created_at,id. Answer the current human
question using the Option, discussion and readonly authoritative source snapshot.

Give a reviewable conclusion, concise reasons, risks and recommendations.
Do not output hidden chain-of-thought. Do not claim to have changed the Option or
code. Describe any proposed refinement in the answer; O5 decides whether to refine.
Never write to the workspace. Scratch/tool caches must stay under TMPDIR.

Return exactly this strict JSON shape with a non-empty human-readable response:
{"schema_version":0,"operation":"discussion","text":"your answer"}
No other keys, Claims, new Options, release, allocation, patch or verdict.
'''
(root / 'prompts' / 'discussion.md').write_text(prompt)
bindings_path.write_text(json.dumps(bindings, indent=2) + '\n')
worker.write_text(source.replace(old, new))
print('Enabled discussion in /opt/scp-workers/codex-v0; existing execution boundary retained.')

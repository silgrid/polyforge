

test("polyforge#24: DAG selects one step and filters progress while preserving reruns and empty state", () => {
  assert.match(source, /const \[selectedStep, setSelectedStep\] = useState<string \| null>\(null\);/);
  assert.match(source, /const \[selectedStepManual, setSelectedStepManual\] = useState\(false\);/);
  assert.match(source, /onClick=\{\(\) => \{ setSelectedStep\(node\.id\); setSelectedStepManual\(true\); \}\}/);
  assert.match(source, /data-pf-step-selected=\{selectedStep === node\.id\}/);
  assert.match(source, /const selectedProgressSnapshot = selectProgressStep\(progressSnapshot, selectedStep\);/);
  assert.match(source, /selectedProgressSnapshot\.lines\.length === 0 \? t\("polyforge\.progress\.notRun"\)/);
  assert.match(source, /function selectProgressStep\(snapshot: PolyforgeProgressSnapshot \| null, step: string \| null\)/);
  assert.match(source, /layout\.blocks\.filter\(\(block\) => block\.step === step\)/);
  assert.match(source, /blocks\.flatMap\(\(block\) => lines\.slice\(block\.first \?\? block\.start, \(block\.first \?\? block\.start\) \+ block\.count\)\)/);
  assert.match(source, /if \(!selectedStepManual\).*currentStep/s);
  assert.match(source, /selectedStep \? selectedStep :/);
  // Existing hover contract remains on the clickable node.
  assert.match(source, /title=\{`\$\{node\.id\} · \$\{stepStatusLabel\(node\.status\)\}\$\{timing \? ` · \$\{formatEventTime\(timing\.startedAt\)\}` : ""\}`\}/);
});

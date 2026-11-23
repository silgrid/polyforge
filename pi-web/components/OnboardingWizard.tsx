"use client";

// polyforge#167 (91-onboarding): the wizard's signatures, zero-value bodies.
// The unit's test commits on top of this and is red on assertions.
import type { OnboardingStep, OnboardingView } from "@/lib/onboarding-state";

export interface OnboardingWizardProps {
  view: OnboardingView;
  cwd: string | null;
  onComplete: (step: OnboardingStep) => void;
  onSkip: (step: OnboardingStep) => void;
  onClose: () => void;
  onOpenAccount: () => void;
  onWorkspaceChange: (cwd: string) => void;
}

export function OnboardingWizard(_props: OnboardingWizardProps) {
  return null;
}

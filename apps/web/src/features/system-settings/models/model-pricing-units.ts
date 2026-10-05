/*
Copyright (C) 2026 LIghtJUNction
*/
import { createContext, useContext } from 'react'

// The fee ratio is credits per token. It is not a fiat amount and must never
// inherit a display currency or the old pricing-unit conversion factor.
export const ModelPricingUnitsContext = createContext(Number.NaN)

export function useModelPricingCreditsPerUsd() {
  return useContext(ModelPricingUnitsContext)
}

export function ratioToUsdPerMillion(ratio: number, creditsPerUsd: number) {
  if (!Number.isFinite(creditsPerUsd) || creditsPerUsd <= 0) {
    throw new Error('Invalid credits per USD')
  }
  return (ratio * 1_000_000) / creditsPerUsd
}

export function usdPerMillionToRatio(priceUSD: number, creditsPerUsd: number) {
  if (!Number.isFinite(creditsPerUsd) || creditsPerUsd <= 0) {
    throw new Error('Invalid credits per USD')
  }
  return (priceUSD * creditsPerUsd) / 1_000_000
}

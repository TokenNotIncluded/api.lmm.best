package service

import (
 "github.com/LIghtJUNction/api.lmm.best/common"
 "github.com/LIghtJUNction/api.lmm.best/pkg/servicetier"
 relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
 "github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
)
func serviceTierUsageQuota(info *relaycommon.RelayInfo,usage *dto.Usage) (int,error) {
 q:=info.ServiceTierQuote;if q==nil || usage==nil {return 0,nil}
 usd,err:=q.CostUSD(servicetier.Usage{Input:usage.PromptTokens,Output:usage.CompletionTokens,Cached:usage.PromptTokensDetails.CachedTokens,CacheWrite:usage.PromptTokensDetails.CacheCreationTokensTotal()})
 if err!=nil {return 0,err}
 return servicetier.Credits(usd,info.ServiceTierCreditsPerUSD,common.MaxQuota)
}

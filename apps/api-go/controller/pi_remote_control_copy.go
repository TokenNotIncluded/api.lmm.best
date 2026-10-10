package controller

import (
	"slices"

	"github.com/LIghtJUNction/api.lmm.best/service"
)

func piRemotePermissionLabels(language string, scopes []string) []string {
	if !slices.Contains(scopes, service.OAuthRemoteControlScope) {
		return nil
	}
	labels := map[string]string{
		"zh":    "远程控制 Pi：发送任务、停止工作和回答插件问题，与所选模型无关；仍需本机启用和会话 PIN。",
		"zh-TW": "遠端控制 Pi：傳送任務、停止工作和回答外掛問題，與所選模型無關；仍需本機啟用和工作階段 PIN。",
		"en":    "Control Pi remotely with any model: send tasks, stop work and answer extension questions. Local enablement and the session PIN are still required.",
		"ja":    "モデルに関係なく Pi を遠隔操作します。タスク送信、停止、拡張機能への回答が可能です。本機での有効化とセッション PIN が必要です。",
		"fr":    "Contrôler Pi à distance avec tout modèle : envoyer des tâches, arrêter et répondre aux extensions. Activation locale et PIN requis.",
		"ru":    "Удалённое управление Pi с любой моделью: задачи, остановка и ответы расширениям. Требуются локальное включение и PIN сеанса.",
		"vi":    "Điều khiển Pi từ xa với mọi mô hình: gửi tác vụ, dừng và trả lời tiện ích. Cần bật trên máy và có mã PIN phiên.",
	}
	label := labels[language]
	if label == "" {
		label = labels["en"]
	}
	return []string{label}
}

//go:build !windows

package hotkey

import (
	"context"

	"github.com/mastererik/translator/internal/logger"
)

// Run — no-op на платформах без глобальных hotkeys (не-Windows).
// Возвращается при отмене ctx.
//
// Сигнатура повторяет windows-версию (r3-4): sessLog принимается, но реальных
// событий здесь нет, поэтому пишем единственный Debug о завершении по отмене.
// sessLog может быть nil — nil-безопасно (см. sessLogDebug ниже). Горутин не
// порождает, ctx-watcher не нужен: ожидание ctx.Done() и есть путь выхода.
//
// Контракт возврата: nil при штатной отмене ctx — единый с windows-версией,
// чтобы pipeline не трактовал завершение по ctx как ошибку (B-9).
func Run(ctx context.Context, onKey func(Key), sessLog logger.SessionLogger) error {
	<-ctx.Done()
	sessLogDebug(sessLog, "hotkey: stub-версия завершена по отмене ctx")
	return nil
}

// sessLogDebug пишет отладочное сообщение в сессионный лог, если он есть.
// Нил-безопасен: sessLog может быть nil (логирование отключено / вызов без
// сессии). Дублирует одноимённый хелпер windows-сборки намеренно — файлы
// взаимоисключающие по build-тегам, поэтому общий (без тега) хелпер невыгоден.
func sessLogDebug(sessLog logger.SessionLogger, msg string) {
	if sessLog != nil {
		_ = sessLog.LogDebug(msg) // _ = err: лог не критичен для работы
	}
}

# English

Go library implementing API for Russian electronic tax senders.


# Что

Go библиотека для работы с онлайн-кассами Умка.

Целевая аудитория - автоматические платежи (онлайн, вендинг).

Патчи с поддержкой других протоколов (Атолл, DTO) приветствуются.

Пожалуйста, пишите по любым вопросам:
- https://github.com/temoto/ru-nalog-go/issues/new
- temotor@gmail.com


## Статус

Готово:
- генератор Go типов для реквизитов ФД из документа с www.nalog.ru
- HTTP API Мещера/Умка: cashboxstatus (состояние кассы), fiscaldoc (запрос документа), fiscalcheck (создание чека), открытие/закрытие смены

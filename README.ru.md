# sap-help-mcp (Go)

MCP-сервер для Claude: живой поиск и чтение **help.sap.com** (документация и
Support Content) и **SAP Community** (блоги, вопросы и ответы), чтобы ответ
брался со страницы, а не из памяти модели.

Один исполняемый файл на Go — без Python, Node и загрузок при запуске.

[English](README.md)

- Учётных данных нет, на диск ничего не пишется: только анонимные GET к
  публичным адресам порталов.
- Ответы кэшируются в памяти на 6 часов, между запросами пауза 200 мс.
- Один инструмент `sap_help` с параметром `action` — одно разрешение в Claude
  Desktop вместо пяти.

## Скачать

Последний релиз: <https://github.com/osipovgoga33/sap-help-mcp/releases/latest>

| Файл | Платформа |
|---|---|
| `sap-help.exe` | Windows x64 |
| `sap-help-darwin-arm64` | macOS Apple Silicon |
| `sap-help-darwin-amd64` | macOS Intel |
| `sap-help-linux-amd64` | Linux x64 |
| `sap-help.mcpb` | расширение Claude Desktop со всеми сборками |

## Установка (Claude Desktop)

**Расширением:** Settings → Extensions → Advanced settings → Install Extension →
выбрать `sap-help.mcpb`. Файлы не подписаны, поэтому Desktop предупредит, что
расширение не проверено.

**Файлом:** положить `sap-help.exe` в постоянное место и добавить в раздел
`mcpServers` файла `claude_desktop_config.json` (Settings → Developer → Edit
Config):

```json
"sap-help": {
  "command": "C:/Tools/MCP/sap-help.exe"
}
```

Слэши в пути — прямые или двойные обратные; одна лишняя или пропущенная запятая,
и Desktop молча пропустит весь файл. Выйти из Desktop через значок в трее
(закрытие окна сервер не останавливает) и запустить снова.

Проверка: попросить Claude вызвать `sap_help` с `action=status`. Первая строка
`%APPDATA%\Claude\logs\mcp-server-sap-help.log` — запущенная версия.

## Инструмент

| action | Что делает | Параметры |
|---|---|---|
| `search` | Поиск по help.sap.com | `query`, `product` (`SAP_ERP`, `SAP_S4HANA_ON_PREMISE`, `SAP_S4HANA_CLOUD`, `SUPPORT_CONTENT`), `version`, `limit` 1–20 |
| `read` | Страница help.sap.com целиком в markdown | `url` из результата поиска, `part` |
| `community_search` | Поиск по SAP Community с собственным ранжированием | `query`, `limit` 1–25, `min_kudos` 0–10 |
| `community_read` | Тред целиком: вопрос и все ответы, принятое решение помечено | `url` или номер сообщения, `part` |
| `status` | Версия, адреса, состояние кэша | — |

Запросы к порталам — на английском; переводить вопрос в терминологию SAP модель
должна сама (это сказано ей в описании инструмента).

## Форум за проверкой Cloudflare

С некоторых сетей (в том числе выходов VPN) community.sap.com отвечает проверкой
Cloudflare «Подтвердите, что вы человек». Программа её не пройдёт, и сервер не
пытается. Вместо ошибки `community_search` и `community_read` возвращают
`status: browser_required` и поле `javascript`:

1. Claude открывает community.sap.com во встроенном браузере Claude Desktop.
2. Если появилась проверка, галочку ставит **пользователь** — Claude это делать
   запрещено.
3. Claude запускает скрипт на этой вкладке: те же запросы LiQL, то же
   ранжирование и тот же вид ответа.

После проверки Cloudflare сервер 30 минут не обращается к форуму напрямую.
help.sap.com это не касается.

## Сборка

Go 1.22+ (zip с go.dev/dl ставится без прав администратора). Внешних модулей
нет, сеть для сборки не нужна.

```powershell
go test ./...
$env:CGO_ENABLED = "0"
go build -trimpath -ldflags "-s -w" -o dist\sap-help.exe .
.\dist\sap-help.exe -version
```

Все платформы и `.mcpb`: `bash build.sh` (для `.mcpb` нужен `zip`).

## Релизы

Релиз собирает GitHub Actions, когда в `main` приходит новая версия. Версия
живёт в трёх местах — `serverVersion` в `main.go`, `version` в
`mcpb/manifest.json`, раздел в `CHANGELOG.md`, — `build.sh` проверяет, что они
совпадают.

## Заметки

- HTML → markdown своим небольшим конвертером: заголовки, списки, ссылки, код,
  таблицы. Сложная вёрстка может выйти грубее.
- Ответ 403/429 от портала — блокировка, и Claude сказано не повторять запрос в
  цикле.
- Эндпоинты help.sap.com не документированы и могут поменяться.
- Только stdio (без HTTP-транспорта).

## Лицензия

[MIT](LICENSE). SAP и названия продуктов SAP — товарные знаки SAP SE; проект с SAP
не связан. Всё содержимое, доступное через сервер, принадлежит SAP SE и авторам;
сервер ничего не хранит и не распространяет.

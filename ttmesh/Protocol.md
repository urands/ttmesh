1. Что такое маршрут в системе

Маршрут (workflow/DAG) — это граф шагов (StepSpec), которые должны выполниться в определённом порядке, с условиями перехода (Edge.condition).
Он хранится вне конверта (в реестре или DHT), а в Envelope кладётся только ссылка (route_ref) и курсоры (step_id, next_step).

Пример:

RouteManifest:
steps: - step_id: "decode_video", task_name: "video.decode" - step_id: "analyze", task_name: "ai.analyze" - step_id: "store", task_name: "storage.write"
edges: - from: "decode_video", to: "analyze" - from: "analyze", to: "store", condition: "status==OK"

2. Что хранится в конверте (для маршрутизации)

В Envelope (каждое сообщение) есть:

meta.route_ref — ссылка на маршрут {route_id, version, hash}.

meta.step_id — текущий шаг (в DAG), который сейчас обрабатывается.

meta.next_step — (опционально) подсказка, куда задача должна пойти после этого шага.

meta.hop_ttl — счётчик переходов (чтобы не зациклиться).

hops[] — массив «хлебных крошек» (HopMeta): кто уже обрабатывал, куда пересылали, какой регион, какое решение приняли.

Таким образом, конверт не тащит весь DAG, а только позицию внутри него + «историю путешествия».

3. Алгоритм поиска исполнителя

Когда узел получает Envelope с meta.step_id = "decode_video", он должен найти исполнителя для этой задачи (task_name = "video.decode" в манифесте маршрута).

Пошагово:

Загрузка спецификации шага
Узел по route_ref подтягивает RouteManifest (если ещё нет в кэше) и находит StepSpec для step_id.

В StepSpec обычно указано:

task_name (какую функцию/способность выполнить),

constraints (например, нужна GPU, регион EU),

shard_key_expr (как вычислить шард-ключ для балансировки).

Поиск кандидатов
Узел смотрит в реестр/DHT (или в таблицу живых узлов), где публикуются capability узлов:

node123: capabilities { "video.decode" }, region=eu-central, load=0.4
node456: capabilities { "video.decode" }, region=us-east, load=0.7

— и отбирает тех, кто удовлетворяет constraint’ам.

Выбор оптимального
Алгоритм зависит от политики:

Гео-близость: выбираем того, у кого geohash ближе к данным/клиенту.

Consistent hashing: по workflow_id или shard_key_expr стабильно назначаем узел.

Нагрузка: берём наименее загруженный.

Часто делают гибрид: score = geo_score + load_penalty.

Назначение
Решение фиксируется:

В конверт добавляется новый HopMeta { node_id=мой, chosen_node_id=target }.

Envelope отправляется в очередь/поток/адрес chosen_node_id.

Исполнитель принимает задачу
Целевой узел получает Envelope, видит, что он назначен именно ему, собирает параметры и выполняет task_name.

4. Как исполнитель узнаёт, что задача именно его

В Envelope, в момент форвардинга, назначается chosen_node_id (и сообщение физически доставляется именно этому узлу, например через брокерскую очередь или по direct QUIC-соединению).

Исполнитель:

проверяет, что meta.step_id соответствует задаче, которую он умеет выполнять (task_name из RouteManifest).

если ок — принимает задачу и начинает обрабатывать.

если constraint не совпадает (например, нет GPU) — может NACK’нуть и вернуть назад.

5. Пример трассировки в hops[]

Пример истории (три хопа):

hops: [
{ node_id="ingress", ts=..., decision="hash=wf123", chosen_node_id="workerA" },
{ node_id="workerA", ts=..., decision="constraint:gpu", chosen_node_id="workerGPU1" },
{ node_id="workerGPU1", ts=..., decision="done", chosen_node_id="" }
]

В первом хопе задача пришла на ingress и была направлена в workerA.

WorkerA понял, что дальше нужен GPU, выбрал workerGPU1.

WorkerGPU1 обработал и завершил.

6. Кому назначается задача в итоге

Назначение делает предыдущий узел, который завершил свой шаг и выбирает, кто исполнит следующий.

Назначенный узел определяется через правила маршрута (StepSpec + Edge.condition) и политику поиска.

Фиксация назначения — это запись в HopMeta и фактическая доставка Envelope в очередь/стрим целевого узла.

✅ Таким образом:

В конверте храним только ссылку на маршрут + текущую позицию + хопы.

Поиск исполнителя — динамический (late binding) на каждом шаге, с учётом geo/constraints/shard.

Задача назначается тому узлу, которого выбрал предыдущий в цепочке, и это фиксируется в HopMeta.

Хочешь, я покажу на псевдокоде алгоритм выбора следующего узла (по geo + нагрузке) и как именно обновляется Envelope при переходе к следующему шагу?

Вы сказали:
покажи
ChatGPT сказал:

Окей! Ниже — компактный, практичный псевдокод (в стиле C++/Go) для:

выбора следующего исполнителя шага (учёт capabilities, constraints, гео-близости, нагрузки, стабильного шардирования),

обновления Envelope и перехода к следующему шагу,

обработки успеха/ошибок и “лизинга” (чтобы не было гонок при назначении).

1. Структуры данных (упрощённо)
   struct NodeInfo {
   std::string node_id;
   std::string region_geohash; // например, "u0..."; чем длиннее общий префикс — тем ближе
   std::set<std::string> capabilities; // {"ai.analyze", "storage.write"}
   std::map<std::string,std::string> labels; // {"gpu":"nvidia","arch":"x86_64"}
   double load01; // 0..1 (скользящее среднее загрузки)
   bool healthy; // по heartbeat
   };

struct StepSpec {
std::string step_id;
std::string task_name; // какую способность ищем
std::vector<std::pair<std::string,std::string>> constraints; // key=value
std::string shard_key_expr; // например "meta.workflow_id" (тут уже вычислено как значение)
uint32_t timeout_ms;
};

struct RouteManifest {
std::string route_id;
uint32_t route_version;
std::string entry_step_id;
std::map<std::string, StepSpec> steps; // step_id -> spec
struct Edge { std::string from, to, condition_expr; };
std::vector<Edge> edges;
};

struct Envelope {
Header header;
Metadata meta; // workflow_id, task_id, route_ref, step_id, next_step, hop_ttl, attrs
std::vector<HopMeta> hops;
// oneof body: invoke/chunk/result/...
};

2. Вспомогательные функции
   Совпадение constraint’ов узла и шага
   bool satisfiesConstraints(const NodeInfo& n, const StepSpec& step) {
   for (auto& [k, v] : step.constraints) {
   auto it = n.labels.find(k);
   if (it == n.labels.end() || it->second != v) return false;
   }
   return true;
   }

Гео-близость по geohash-префиксу
// длина общего префикса двух geohash-строк
int commonPrefixLen(const std::string& a, const std::string& b) {
int L = std::min(a.size(), b.size());
int i = 0; while (i < L && a[i] == b[i]) ++i; return i;
}
// нормируем 0..1 (пусть 8 символов префикса считаем "очень близко")
double geoScore(const std::string& a, const std::string& b) {
return std::min(1.0, commonPrefixLen(a, b) / 8.0);
}

Стабильное шардирование (rendezvous hashing)
// хэш (например, xxh3/CityHash); здесь — заглушка
uint64_t h64(const std::string& s);

double rendezvousScore(const NodeInfo& n, const std::string& shard_key, const std::string& task_name) {
// для стабильности учитываем и имя задачи
uint64_t h = h64(n.node_id + "|" + shard_key + "|" + task_name);
// превращаем в (0,1]; чем больше — тем “выигрышнее” узел
return (h / (double)std::numeric_limits<uint64_t>::max());
}

3. Основной выбор узла (смешанный скоринг)

Идея: сначала фильтруем по capability/здоровью/constraint’ам, затем скорим кандидатов:

S_geo — ближе по региону,

S_rdv — стабильность выбора для одного и того же shard_key,

S_load — штраф за нагрузку (чем больше загрузка, тем хуже).

Весовая формула:

S = w*geo * S*geo + w_rdv * S_rdv + w_load \* (1 - load01)

где w_geo, w_rdv, w_load задаются политикой (например, 0.5 / 0.3 / 0.2).

struct PickPolicy {
double w_geo = 0.5;
double w_rdv = 0.3;
double w_load = 0.2;
std::string caller_geohash; // гео источника/данных (для geoScore)
};

std::optional<NodeInfo> selectNextNode(
const StepSpec& step,
const std::vector<NodeInfo>& allNodes,
const std::string& shard_key,
const PickPolicy& policy)
{
std::optional<NodeInfo> best;
double bestScore = -1.0;

for (auto& n : allNodes) {
if (!n.healthy) continue;
if (!n.capabilities.count(step.task_name)) continue;
if (!satisfiesConstraints(n, step)) continue;

    double S_geo = geoScore(policy.caller_geohash, n.region_geohash);
    double S_rdv = rendezvousScore(n, shard_key, step.task_name);
    double S_load = 1.0 - std::clamp(n.load01, 0.0, 1.0);

    double S = policy.w_geo * S_geo + policy.w_rdv * S_rdv + policy.w_load * S_load;

    if (S > bestScore) { bestScore = S; best = n; }

}
return best;
}

Примечание: если кандидатов нет — возвращаем nullopt (будет RETRY/NACK), либо снижаем требования (fallback).

4. “Лизинг” слота у выбранного узла (чтобы избежать гонок)

Перед тем как реально форвардить Envelope, запрашиваем у узла короткую бронь на выполнение шага.

struct Lease {
bool granted;
std::string lease_id;
int64_t lease_deadline_ms;
};

// RPC/сообщение к узлу-кандидату: “держи мне 1 слот под workflow_id/step_id”
Lease requestLease(const NodeInfo& node, const std::string& workflow_id,
const std::string& step_id, uint32_t timeout_ms);

Если granted == false (узел перегружен/уже занят) — выбираем следующего в рейтинге. Это резко снижает вероятность, что два “раутера” назначат одну и ту же задачу разным исполнителям.

5. Обновление Envelope и форвардинг

После успешного Lease.granted:

уменьшаем hop_ttl,

добавляем HopMeta с принятым решением,

обновляем курсор (meta.step_id = next_step на этапе перехода либо оставляем текущий до приёма на новом узле — оба подхода допустимы; ниже — передаём как есть, а новый узел сам поставит step_id на себя при “start step”),

отправляем Envelope целевому узлу (в его очередь/адрес/стрим).

bool forwardToNext(
Envelope& env,
const RouteManifest& route,
const StepSpec& current_step, // шаг, который мы ТОЛЬКО ЧТО закончили
const std::string& next_step_id,
const NodeInfo& chosen_node,
const std::string& my_node_id,
const std::string& my_region)
{
if (env.meta.hop_ttl == 0) return false;

// 1) HopMeta
HopMeta hop;
hop.node_id = my_node_id;
hop.ts_unix_ms = now_ms();
hop.region = my_region;
hop.decision = "geo+rdv+load"; // краткая причина
hop.chosen_node_id = chosen_node.node_id;
hop.rtt_ms = 0;
env.hops.push_back(std::move(hop));
if (env.hops.size() > 16) {
// оставляем последние 16 (ring buffer)
env.hops.erase(begin(env.hops), begin(env.hops) + (env.hops.size() - 16));
}

// 2) TTL
env.meta.hop_ttl -= 1;

// 3) Курсор
env.meta.next_step = next_step_id; // подсказка
// Вариант A: устанавливать step_id немедленно:
// env.meta.step_id = next_step_id;
// Вариант B (часто удобней): новый исполнитель при старте шага подтвердит step_id=next_step_id

// 4) Физически доставить Envelope узлу chosen_node (через брокер/NATS/QUIC):
return sendToNode(chosen_node.node_id, env); // реализация транспорта — ваша
}

6. Выбор следующего шага по условиям (edges)

После выполнения текущего шага узел формирует Result(final=true) и решает, куда дальше:

находит все edges с edge.from == current_step.step_id,

оценивает condition_expr (если пусто — “true”),

для первого истинного — next_step = edge.to.

std::optional<std::string> pickNextStepId(const RouteManifest& route,
const std::string& current*step_id,
const Result& step_result /*логика условий\_/)
{
for (auto& e : route.edges) {
if (e.from != current_step_id) continue;
if (e.condition_expr.empty() || evalCondition(e.condition_expr, step_result)) {
return e.to;
}
}
return std::nullopt; // нет продолжения — финал workflow
}

7. Общий цикл “завершил шаг → выбрал узел → форварднул”
   void onStepFinished(Envelope& env,
   const RouteManifest& route,
   const Result& step_result,
   const std::vector<NodeInfo>& registry,
   const PickPolicy& policy,
   const std::string& my_node_id,
   const std::string& my_region)
   {
   auto cur_id = env.meta.step_id;
   auto next_id_opt = pickNextStepId(route, cur_id, step_result);

if (!next_id_opt) {
// Финал workflow: шлём итог на reply_to и выходим
sendFinalToReplyTo(env, step_result);
return;
}

const auto next_id = \*next_id_opt;
const StepSpec& next_step = route.steps.at(next_id);

// Вычисляем shard_key (здесь предположим: это просто workflow_id)
const std::string shard_key = env.meta.workflow_id;

// 1) Выбор узла
auto node_opt = selectNextNode(next_step, registry, shard_key, policy);
if (!node_opt) {
// нет кандидатов → Retry/NACK
nackOrRetry(env, "NO_CANDIDATE");
return;
}

// 2) Лизинг
auto lease = requestLease(\*node_opt, env.meta.workflow_id, next_id, next_step.timeout_ms);
if (!lease.granted) {
// попробуем следующего в рейтинге (упражнение — перебор нескольких top-K)
nackOrRetry(env, "LEASE_DENIED");
return;
}

// 3) Форвардинг (обновляем Envelope и отправляем)
bool ok = forwardToNext(env, route, route.steps.at(cur_id), next_id, *node_opt, my_node_id, my_region);
if (!ok) {
releaseLease(*node_opt, lease.lease_id); // отдадим бронь
nackOrRetry(env, "FORWARD_FAILED");
return;
}

// 4) Бронь будет считаться использованной, когда целевой узел подтвердит старт шага.
}

8. Что именно хранится в конверте в процессе

Постоянно: meta.route_ref, meta.workflow_id, meta.task_id, meta.hop_ttl, header.reply_to.

Меняется: meta.step_id (текущий шаг), meta.next_step (подсказка), hops[] (лента решений).

Назначение: отражается в последнем HopMeta (chosen_node_id) и фактом доставки конверта в очередь/адрес выбранного узла.

9. Ошибки/ретраи

Если выбранный узел отказал/упал, предыдущий узел:

либо пробует следующего по рейтингу (переназначение),

либо отдаёт Result{status=ST_RETRY}/Ack NACK на reply_to, чтобы инициатор/оркестратор принял решение (exponential backoff, смена политики и т.п.).

10. Итого

Решение о назначении делает узел, заканчивающий шаг (late binding).

В Envelope меняются только курсор и хопы — весь маршрут остаётся “по ссылке”.

Выбор исполнителя — фильтр по capability/constraints/здоровью → скоринг (гео+шард+нагрузка) → лизинг → форвардинг.

Это даёт устойчивое распределение, низкую латентность и маленький overhead в конверте.

Если хочешь — могу прислать реф-имплементацию этих функций на Go (с quic-go) или на C++20 (asio) с заглушками requestLease()/sendToNode().

Думал на протяжении 8s

Окей! Ниже — два варианта в виде C++-структур:

Полный конверт (Envelope) — «богатый» формат (идеологически соответствует protobuf-схеме).

Короткий ответ (TinyResult) — сверхкомпактный фрейм для маленьких ответов (несколько байт).

Явный on-wire формат для Envelope обычно делают через Protobuf/CBOR/FlatBuffers; ниже — «POJO»-структуры с подробными полями и комментариями. Для TinyResult указан точный байтовый порядок и битовые флаги.

1. Полный конверт (Envelope)
   #pragma once
   #include <cstdint>
   #include <string>
   #include <vector>
   #include <map>
   #include <optional>

namespace mesh::v1 {

// =========== Общие типы ===========

enum class Codec : uint8_t {
RAW = 0, PROTOBUF = 1, CAPNPROTO = 2, FLATBUFFERS = 3
};

enum class Compression : uint8_t {
NONE = 0, ZSTD = 1, SNAPPY = 2
};

enum class Status : uint8_t {
OK = 0, PARTIAL = 1, RETRY = 2, FAILED = 3, CANCELED = 4, TIMEOUT = 5
};

enum class AckCode : uint8_t {
OK = 0, NACK = 1
};

enum class FlowScope : uint8_t {
PARAM = 0, STEP = 1, WORKFLOW = 2
};

struct BlobRef {
std::string id; // контент-адрес (напр. "sha256:..."), обязателен
uint64_t size = 0; // размер в байтах (если известен)
std::string media_type; // MIME, напр. "application/x-protobuf;msg=MyMsg"
std::string schema_ref; // ссылка на схему (buf.build/... или registry)
std::string integrity_alg; // "sha256"/"blake3" (опц.)
std::vector<uint8_t> integrity; // хэш целого объекта (опц.)
};

struct Metrics {
uint64_t cpu_ms = 0;
uint64_t wall_ms = 0;
uint64_t mem_peak_bytes = 0;
std::map<std::string, double> custom; // произвольные метрики
};

struct LogLine {
int64_t ts_unix_ms = 0; // UNIX-время, мс
std::string level; // "INFO"/"WARN"/"ERROR"/"DEBUG"
std::string text; // сообщение лога
};

// =========== Маршрутизация ===========

struct RouteRef {
std::string route_id; // ID маршрута
uint32_t route_version=0; // версия маршрута
std::string route_hash; // контент-хэш манифеста (для кэша/целостности)
};

struct Metadata {
std::string workflow_id; // ID workflow (шард-ключ)
std::string task_id; // ID вызова внутри workflow
RouteRef route_ref; // ссылка на маршрут (обяз.)
std::string step_id; // текущий шаг (курсор)
std::string next_step; // следующий шаг (подсказка; опц.)
uint32_t hop_ttl = 0; // TTL против циклов (уменьшается на каждом хопе)
int64_t deadline_unix_ms = 0; // общий дедлайн по workflow (опц.; 0 — нет)
std::map<std::string, std::string> attrs; // произвольные атрибуты (tenant, priority)
};

struct HopMeta {
std::string node_id; // кто обрабатывал/решал
int64_t ts_unix_ms = 0; // метка времени
std::string region; // зона/гео/геохэш
std::string decision; // кратко: "geo+hash", "constraint:gpu", и т.п.
std::string chosen_node_id;// куда переслали дальше (если было)
uint32_t rtt_ms = 0; // опц.
std::vector<uint8_t> sig; // подпись хопа (опц.)
};

// =========== Заголовок ===========

struct Header {
std::string message_id; // ULID/UUID (уникален для ретраев)
std::string correlation_id; // связывает chunk/result с исходным invoke
std::string trace_id; // для распределённого трейсинга
uint32_t protocol_version=1; // версия протокола (должна быть 1)
Codec codec = Codec::PROTOBUF;
Compression compression = Compression::NONE;
int64_t created_unix_ms = 0; // UNIX-время создания, мс
std::string reply_to; // куда слать Result/Ack/Control (topic/URI) (может быть пусто)
std::map<std::string, std::string> tags; // произвольные теги
};

// =========== Вызов и параметры ===========

struct ParamDescriptor {
std::string name; // имя параметра (уникально в рамках invoke)
std::string media_type; // MIME типа входа
std::string schema_ref; // ссылка на схему (если есть)
bool streamed = false; // true => придут чанки ParamChunk
uint64_t total_size = 0; // общий размер (если известен заранее)
bool required = true; // обязателен ли параметр
std::string integrity_alg;// "sha256"/"blake3" (опц.)
std::vector<uint8_t> integrity; // хэш целого параметра (опц.)
};

struct Invoke {
std::vector<ParamDescriptor> params; // декларация входов
std::map<std::string, std::vector<uint8_t>> inline_args; // мелкие параметры (<= ~64 KiB)
std::map<std::string, BlobRef> blobs; // ссылки на большие входы (CAS/S3)
};

struct ParamChunk {
std::string param_name; // имя из ParamDescriptor
uint64_t index = 0; // порядковый номер (0..N)
uint64_t offset = 0; // байтовый сдвиг
bool last = false; // последний ли чанк параметра
std::vector<uint8_t> data; // полезная нагрузка
uint32_t crc32 = 0; // опц., локальная проверка чанка
};

// =========== Результаты/управление ===========

struct Output {
std::string name; // имя выхода
std::optional<std::vector<uint8_t>> inline_data; // неб. результат
std::optional<BlobRef> blob; // ссылка на большой результат
};

struct Result {
Status status = Status::OK; // OK/PARTIAL/FAILED/...
std::vector<Output> outputs; // выход(ы) шага
std::vector<LogLine> logs; // логи (можно слать PARTIAL)
Metrics metrics; // метрики
std::string error_code; // код ошибки (при FAILED/\*)
std::string error_msg; // текст ошибки
bool final = false; // финал по текущему step_id?
float progress = 0.0f; // 0..1 (опц.)
};

struct FlowControl {
FlowScope scope = FlowScope::PARAM; // область кредита
uint32_t credit = 0; // сколько ещё чанков можно прислать
};

struct Cancel { std::string reason; };

struct Control {
std::optional<FlowControl> flow; // либо
std::optional<Cancel> cancel; // либо
};

struct Ack {
std::string message_id; // подтверждаемый message_id
AckCode code = AckCode::OK;
std::string reason; // при NACK
};

// =========== Единица передачи (Envelope) ===========

struct Envelope {
Header header; // обязательный заголовок
Metadata meta; // курсор маршрута/сроки/атрибуты
std::vector<HopMeta> hops; // короткая лента последних хопов

    // «oneof» — заполняется ровно одно из полей ниже:
    std::optional<Invoke> invoke;
    std::optional<ParamChunk> chunk;
    std::optional<Result> result;
    std::optional<Ack> ack;
    std::optional<Control> control;

};

} // namespace mesh::v1

Пояснения по Envelope:

Header.protocol_version всегда 1.

meta.route_ref обязателен (маршрут берём из реестра/DHT).

meta.step_id — текущий шаг; после завершения шага и принятия решения о переходе следующий узел обновит курсор и переслёт далее.

hops — только несколько последних записей (для компактности).

Сериализация на провод — обычно Protobuf/CBOR/FlatBuffers (эти struct — «карта полей»).

2. Короткий ответ (TinyResult) — компактный фрейм

Эта форма идёт на том же QUIC-стриме, где уже был выполнен SESSION_INIT (контекст: workflow, step, reply_to и т.п.). Tiny-фрейм несёт только минимум: идентификаторы на стриме, флаги и полезную нагрузку.

Формат на проводе (порядок полей):

u32 scid // session context id (локален для стрима; задан в SESSION_INIT)
var seq // varint: локальный порядковый номер сообщения на стриме
u8 hdr // биты: [FINAL | HAS_NAME_IDX | Rsv | STATUS(3b) | PAYLOAD_KIND(2b)]
[ var name_idx ] // если HAS_NAME_IDX=1: индекс имени из словаря (из SESSION_INIT)
-- payload selector --
if PAYLOAD_KIND==1: // inline bytes
var payload_len
u8 payload[payload_len]
else if PAYLOAD_KIND==2: // short blob id
var blob_id_len
u8 blob_id[blob_id_len] // например укороченный id (8–16 байт)

Интерпретация битов hdr:

bit7 (0x80): FINAL — финальный ответ по текущему шагу.

bit6 (0x40): HAS_NAME_IDX — есть индекс имени выхода.

bit5 (0x20): зарезервировано = 0.

bits4..2 (0x1C): STATUS — OK=0, PARTIAL=1, FAILED=2, RETRY=3, CANCELED=4, TIMEOUT=5.

bits1..0 (0x03): PAYLOAD_KIND — 0=none, 1=inline bytes, 2=blobref-id short.

C++ описание как helper-структуры:

namespace mesh::v1::tiny {

// Тип полезной нагрузки
enum class PayloadKind : uint8_t { NONE = 0, INLINE_BYTES = 1, BLOB_ID_SHORT = 2 };

// Заголовок в терминах полей (битовую упаковку/распаковку делаем в кодеке)
struct TinyResult {
uint32_t scid = 0; // session context id (из SESSION_INIT)
uint64_t seq = 0; // локальный порядковый номер (varint на проводе)
bool final = false; // бит FINAL
bool has_name_idx = false; // бит HAS_NAME_IDX
Status status = Status::OK; // 3 бита STATUS
PayloadKind kind = PayloadKind::NONE; // 2 бита PAYLOAD_KIND

    // Если has_name_idx==true — индекс имени выхода (экономит байты вместо строки)
    std::optional<uint64_t> name_idx;

    // Полезная нагрузка (только один вид)
    std::vector<uint8_t> inline_payload; // если kind==INLINE_BYTES
    std::vector<uint8_t> blob_id_short;  // если kind==BLOB_ID_SHORT (укороченный id)

    // Удобный конструктор короткого «OK, финал, N байт»
    static TinyResult ok_final_bytes(uint32_t sc, uint64_t s, std::vector<uint8_t> bytes) {
        TinyResult r;
        r.scid = sc; r.seq = s;
        r.final = true; r.has_name_idx = false;
        r.status = Status::OK;
        r.kind = PayloadKind::INLINE_BYTES;
        r.inline_payload = std::move(bytes);
        return r;
    }

};

} // namespace mesh::v1::tiny

Минимальный размер на проводе (оценка):

scid (u32) = 4 байта

seq (varint, для маленьких значений) ≈ 1 байт

hdr = 1 байт

payload_len (varint) + payload (например 1 байт) = 1 + 1
Итого ~7 байт для «OK + финал + 1 байт данных».
Без имени (name_idx) и без blob-id.

Примечания по сериализации

Envelope: используйте Protobuf/CBOR — эти struct соответствуют полям, порядок/varint возьмёт на себя библиотека.

TinyResult: это «ручной» фрейм — нужен небольшой кодек:

varint-кодирование для seq, payload_len, name_idx, blob_id_len;

сборка/разбор hdr через маски:

uint8_t hdr = 0;
hdr |= (final ? 0x80 : 0);
hdr |= (has_name_idx ? 0x40 : 0);
hdr |= (static_cast<uint8_t>(status) & 0x07) << 2; // bits4..2
hdr |= (static_cast<uint8_t>(kind) & 0x03); // bits1..0

Порядок байтов: числовые поля вне varint (например scid) — network byte order (big-endian).

Если хочешь, добавлю такие же «компактные» структуры для TinyAck и TinyControl (FLOW/CANCEL) + пример кодека varint/битов и round-trip тест.

Вы сказали:
а можем мы это реализовать в виде структури на си языка
ChatGPT сказал:

Да, можно. Ниже — аккуратная C99-версия «полного конверта» (Envelope) и «короткого ответа» (TinyResult).
Без классов и STL: только фиксированные enum, массивы/указатели + длины, флаги присутствия и union для oneof.

Примечание: строки представлены как uint8_t\* data + size_t len. «Карты» заменены на массивы пар key/value.

Общие типы (mesh.h)
#pragma once
#include <stdint.h>
#include <stddef.h>
#include <stdbool.h>

/_ ===== Enums ===== _/
typedef enum {
CODEC_RAW = 0, CODEC_PROTOBUF = 1, CODEC_CAPNPROTO = 2, CODEC_FLATBUFFERS = 3
} mesh_codec_t;

typedef enum {
COMP_NONE = 0, COMP_ZSTD = 1, COMP_SNAPPY = 2
} mesh_comp_t;

typedef enum {
ST_OK = 0, ST_PARTIAL = 1, ST_RETRY = 2, ST_FAILED = 3, ST_CANCELED = 4, ST_TIMEOUT = 5
} mesh_status_t;

typedef enum {
ACK_OK = 0, ACK_NACK = 1
} mesh_ack_code_t;

typedef enum {
FLOW_PARAM = 0, FLOW_STEP = 1, FLOW_WORKFLOW = 2
} mesh_flow_scope_t;

/_ ===== Сырые байты/строки ===== _/
typedef struct { uint8_t\* data; size_t len; } mesh_bytes_t;

/_ Пара "ключ-значение" для простых словарей _/
typedef struct { mesh_bytes_t key; mesh_bytes_t val; } mesh_kv_t;

/_ ===== BlobRef ===== _/
typedef struct {
mesh_bytes_t id; /_ напр. "sha256:..." _/
uint64_t size; /_ 0 если неизвестен _/
mesh_bytes_t media_type; /_ MIME _/
mesh_bytes_t schema_ref; /_ ссылка на схему _/
mesh_bytes_t integrity_alg;/_ "sha256"/"blake3" _/
mesh_bytes_t integrity; /_ хэш объекта _/
} mesh_blobref_t;

/_ ===== Метрики/Логи ===== _/
typedef struct {
uint64_t cpu_ms;
uint64_t wall_ms;
uint64_t mem_peak_bytes;
mesh_kv_t* custom; size_t custom_len; /* произвольные метрики \*/
} mesh_metrics_t;

typedef struct {
int64_t ts_unix_ms;
mesh_bytes_t level; /_ "INFO"/... _/
mesh_bytes_t text;
} mesh_logline_t;

/_ ===== RouteRef / Metadata / HopMeta ===== _/
typedef struct {
mesh_bytes_t route_id;
uint32_t route_version;
mesh_bytes_t route_hash;
} mesh_route_ref_t;

typedef struct {
mesh_bytes_t workflow_id;
mesh_bytes_t task_id;
mesh_route_ref_t route_ref;
mesh_bytes_t step_id; /_ текущий шаг _/
mesh_bytes_t next_step; /_ опционально _/
uint32_t hop_ttl; /_ TTL против циклов _/
int64_t deadline_unix_ms; /_ 0 если нет дедлайна _/
mesh_kv_t* attrs; size_t attrs_len; /* произвольные атрибуты \*/
} mesh_metadata_t;

typedef struct {
mesh_bytes_t node_id;
int64_t ts_unix_ms;
mesh_bytes_t region;
mesh_bytes_t decision;
mesh_bytes_t chosen_node_id;
uint32_t rtt_ms; /_ 0 если неизвестно _/
mesh_bytes_t sig; /_ подпись хопа (опц.) _/
} mesh_hopmeta_t;

/_ ===== Header ===== _/
typedef struct {
mesh_bytes_t message_id; /_ ULID/UUID _/
mesh_bytes_t correlation_id; /_ связывает chunk/result с invoke _/
mesh_bytes_t trace_id; /_ трейсинг _/
uint32_t protocol_version;/_ всегда 1 для этой спецификации _/
mesh_codec_t codec;
mesh_comp_t compression;
int64_t created_unix_ms;
mesh_bytes_t reply_to; /_ topic/URI (может быть пусто) _/
mesh_kv_t\* tags; size_t tags_len;
} mesh_header_t;

/_ ===== Invoke / Params / Chunk ===== _/
typedef struct {
mesh_bytes_t name; /_ имя параметра _/
mesh_bytes_t media_type; /_ MIME _/
mesh_bytes_t schema_ref; /_ ссылка на схему _/
bool streamed; /_ true => придут чанки _/
uint64_t total_size; /_ 0 если неизвестно _/
bool required; /_ обязателен ли параметр _/
mesh_bytes_t integrity_alg; /_ "sha256"/"blake3" _/
mesh_bytes_t integrity; /_ хэш параметра _/
} mesh_param_desc_t;

typedef struct {
mesh_param_desc_t* params; size_t params_len; /* декларации _/
mesh_kv_t_ inline_args; size_t inline_args_len; /_ ключ -> байты _/
/_ blobs: ключ -> BlobRef _/
struct { mesh_bytes_t key; mesh_blobref_t val; } \*blobs;
size_t blobs_len;
} mesh_invoke_t;

typedef struct {
mesh_bytes_t param_name; /_ имя из ParamDescriptor _/
uint64_t index; /_ 0..N _/
uint64_t offset; /_ байтовое смещение _/
bool last; /_ последний? _/
mesh_bytes_t data; /_ payload _/
uint32_t crc32; /_ 0 если не используется _/
} mesh_param_chunk_t;

/_ ===== Result / Control / Ack ===== _/
typedef struct {
mesh_bytes_t name;
/_ oneof payload: либо inline_data, либо blob (по флагам) _/
bool has_inline_data;
mesh_bytes_t inline_data;
bool has_blob;
mesh_blobref_t blob;
} mesh_output_t;

typedef struct {
mesh_status_t status;
mesh_output_t* outputs; size_t outputs_len;
mesh_logline_t* logs; size_t logs_len;
mesh_metrics_t metrics;
mesh_bytes_t error_code;
mesh_bytes_t error_msg;
bool final; /_ финал по текущему step_id _/
float progress; /_ 0..1 _/
} mesh_result_t;

typedef struct {
mesh_flow_scope_t scope;
uint32_t credit; /_ сколько ещё чанков можно прислать _/
} mesh_flow_control_t;

typedef struct { mesh_bytes_t reason; } mesh_cancel_t;

typedef struct {
bool has_flow;
mesh_flow_control_t flow;
bool has_cancel;
mesh_cancel_t cancel;
} mesh_control_t;

typedef struct {
mesh_bytes_t message_id; /_ подтверждаемый message_id _/
mesh_ack_code_t code; /_ OK/NACK _/
mesh_bytes_t reason; /_ при NACK _/
} mesh_ack_t;

/_ ===== Envelope: oneof body ===== _/
typedef enum {
BODY_NONE = 0,
BODY_INVOKE = 1,
BODY_CHUNK = 2,
BODY_RESULT = 3,
BODY_ACK = 4,
BODY_CONTROL = 5
} mesh_body_kind_t;

typedef struct {
mesh_header_t header;
mesh_metadata_t meta;
mesh_hopmeta_t* hops; size_t hops_len; /* храните только последние K (напр. 16) \*/

    mesh_body_kind_t body_kind;
    union {
        mesh_invoke_t      invoke;
        mesh_param_chunk_t chunk;
        mesh_result_t      result;
        mesh_ack_t         ack;
        mesh_control_t     control;
    } body;

} mesh_envelope_t;

Компактный «короткий ответ» (TinyResult) для маленьких payload’ов

Этот фрейм идёт на том же QUIC-стриме после SESSION_INIT (контекст установлен один раз).
Поля упаковываются вручную: varint для чисел, битовые флаги в одном байте заголовка.

/\* === TinyResult on-wire header bits ===

- hdr:
- bit7 : FINAL
- bit6 : HAS_NAME_IDX
- bit5 : RESERVED = 0
- bits4..2 : STATUS (0..5) // mesh_status_t
- bits1..0 : PAYLOAD_KIND // 0=NONE,1=INLINE_BYTES,2=BLOB_ID_SHORT
  \*/
  typedef enum {
  TPK_NONE = 0,
  TPK_INLINE_BYTES = 1,
  TPK_BLOB_ID_SHORT = 2
  } mesh_tiny_payload_kind_t;

typedef struct {
uint32_t scid; /_ session context id (из SESSION_INIT) _/
uint64_t seq; /_ локальный последовательный номер (varint на проводе) _/

    /* заголовочные флаги */
    bool            final;         /* bit7 */
    bool            has_name_idx;  /* bit6 */
    mesh_status_t   status;        /* bits4..2 */
    mesh_tiny_payload_kind_t kind; /* bits1..0 */

    /* имя выхода по индексу словаря (если has_name_idx) */
    bool     has_name_index;
    uint64_t name_index;  /* varint */

    /* полезная нагрузка */
    mesh_bytes_t inline_payload; /* если kind==TPK_INLINE_BYTES */
    mesh_bytes_t blob_id_short;  /* если kind==TPK_BLOB_ID_SHORT (8..16 байт id) */

} mesh_tiny_result_t;

/_ Упаковка hdr в один байт _/
static inline uint8_t mesh_tiny_hdr_pack(bool final, bool has_name_idx,
mesh_status_t st, mesh_tiny_payload_kind_t kind)
{
uint8_t b = 0;
if (final) b |= 0x80;
if (has_name_idx) b |= 0x40;
/_ bits4..2: status (ограничить 3 битами) _/
b |= ((uint8_t)st & 0x07) << 2;
/_ bits1..0: kind (2 бита) _/
b |= ((uint8_t)kind & 0x03);
return b;
}

/_ Распаковка hdr _/
static inline void mesh_tiny_hdr_unpack(uint8_t b, bool* final, bool* has_name_idx,
mesh_status_t* st, mesh_tiny_payload_kind_t* kind)
{
if (final) *final = (b & 0x80) != 0;
if (has_name_idx) *has_name_idx = (b & 0x40) != 0;
if (st) *st = (mesh_status_t)((b >> 2) & 0x07);
if (kind) *kind = (mesh_tiny_payload_kind_t)(b & 0x03);
}

Минимальный размер на проводе (оценочно):

scid (u32, big-endian) — 4 байта

seq (varint, маленькое значение) — 1 байт

hdr — 1 байт

payload_len (varint) + payload (например 1 байт) — 1 + 1
Итого ~7 байт для «OK + FINAL + 1-байтовый ответ».

Замечания по сериализации

Для Envelope используйте Protobuf/CBOR/FlatBuffers. Эти структуры — «карта полей» и размеры.

Для TinyResult нужен небольшой кодек:

varint-кодирование (uint64_t → 1–10 байт),

упаковка/распаковка hdr (см. helpers выше),

сетевой порядок байт для scid (big-endian).

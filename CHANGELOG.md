# Changelog

## [0.36.0](https://github.com/this-is-tobi/rta/compare/v0.35.0...v0.36.0) (2026-10-05)


### ⚠ BREAKING CHANGES

* **tui:** a on the queue no longer opens the form (A does), and x on a roster row revokes at once instead of opening the form.
* **tui:** from a cold start p, t, f, b, H and q are now letters of a search; select a tile first, or use : to browse and ctrl+c to quit.
* **mcp:** a parked call that nobody answered returns core.consent.expired to the agent instead of core.grant.required; the record's code is unchanged.
* **builtin:** the shared keys plugins.net.timeout, plugins.fs.limit, plugins.fs.depth and plugins.audit.timeout are no longer read; a config file or profile that sets one sets nothing until it names the capability.
* **net:** `rta net info` is `rta net overview`, the MCP tool net_info is net_overview, and a grant, lock, role or dashboard entry naming net.info names net.overview now.
* **note:** the id of note.done, note.reopen and note.rm is an array of strings instead of an integer, so an MCP call sends {"id": ["3"]}, and note.done.notatodo is no longer an error.
* **agent:** `agent overview` has no `refused` row; `needs your grant`, `malformed or unknown calls` and `refused otherwise` replace it.
* **grant:** `grant list -o json` is always a single table; the roles in force are on `grant list --detail`, and the table has an `Expires At` column.
* **init:** `rta init` no longer asks for an output format or dashboard tiles and no longer writes the config file; use `rta dashboard add` or `+` in the TUI for tiles. Without a terminal it exits 3 instead of 1 when it has registrations to ask about.
* **doctor:** `rta doctor` exits 1 when a row is an error (it exited 0 whatever it found), and reports first-run emptiness as ok where it reported info.
* **app:** the `approvals` key of the `plugin untrust` answer is now `approvals withdrawn` (`approvals to withdraw` under --dry-run), and a new `approvals left` key follows it.
* **paths:** on macOS rta reads $XDG_CONFIG_HOME/rta or ~/.config/rta instead of ~/Library/Application Support/rta. Move the files with the command `rta doctor` prints.

### Features

* **agent:** allow shows the call it is releasing, and says when it cannot work ([00f08fc](https://github.com/this-is-tobi/rta/commit/00f08fc7aa4e4d37a54ee9393b82294b99bd5dbb))
* **agent:** the log is one line per call, filterable, and says how many it left out ([4435418](https://github.com/this-is-tobi/rta/commit/443541852ac367a345e935158b6a1a961909a4f1))
* **app:** `plugin install` of a first-party plugin offers to attach the first-party index ([93024e5](https://github.com/this-is-tobi/rta/commit/93024e5f76d7d577c2c6843f21b653970a3e820d))
* **app:** a destructive command at a terminal shows what it would do and asks ([2701606](https://github.com/this-is-tobi/rta/commit/2701606d899197401848be084605aafb39230faf))
* **app:** a wrong guess is answered with the command that means it ([ee9be5d](https://github.com/this-is-tobi/rta/commit/ee9be5d9220fa4fd7f819775ef09dfcc95ae7fdb))
* **app:** help wraps at the terminal, opens with where to start, and lists flags that apply ([8dcb656](https://github.com/this-is-tobi/rta/commit/8dcb656c81c25529288b551cdb2f5a8ea9540691))
* **app:** plugin list and doctor keep a row for a plugin that failed to start ([e24a7dd](https://github.com/this-is-tobi/rta/commit/e24a7ddeabb3198037cf614adbba934fbd9470f3))
* **app:** pluginWordHint names a first-party or broken plugin where a command was typed ([b12f817](https://github.com/this-is-tobi/rta/commit/b12f8177d4650b0d70646055691e7a69ff43a2c5))
* **app:** profile rm and the plugin removals ask at a terminal, with their own preview ([9ba5368](https://github.com/this-is-tobi/rta/commit/9ba53681a986b6d1a095ea7ccfdabc6183327e14))
* **app:** the daily commands show examples under --help ([b84579e](https://github.com/this-is-tobi/rta/commit/b84579ec4e075282e1e4d6160f7c76949559191e))
* **app:** the foot of rta --help says what to type first ([1e42056](https://github.com/this-is-tobi/rta/commit/1e4205619f39f685a1dde289e3856a9f0ceba876))
* **app:** verb synonyms are hidden aliases everywhere, and rta lock &lt;name&gt; means lock add ([160117d](https://github.com/this-is-tobi/rta/commit/160117d411988a292babbdb512dfffc3abcf1846))
* **audit:** say where rta is registered, from the files audit clients already reads ([f84fa51](https://github.com/this-is-tobi/rta/commit/f84fa51b041850a2eb4cee08a8b8cbd7800f630c))
* **builtin:** a setting that means different things to different commands is keyed to one ([7e83d26](https://github.com/this-is-tobi/rta/commit/7e83d2692eb0e30a8d981b502dbeb8f5a6918e60))
* **cli:** a capability's declared examples are printed in --help and on the explain card ([a6ad128](https://github.com/this-is-tobi/rta/commit/a6ad1283e730402ce6cab8e7a913665f2f72380a))
* **cli:** a capability's search keywords find it in explain and in an unknown command's hint ([f617cc5](https://github.com/this-is-tobi/rta/commit/f617cc58409ed5e10bc43b74d6de40710b380ad9))
* **cli:** a command that takes several ids says so, and its completion drops what is typed or gone ([6956f44](https://github.com/this-is-tobi/rta/commit/6956f4493de64e6c803fb15a3b4db799ad33d7bd))
* **cli:** a declared one-letter flag is registered, and a duration is a flag of that type ([878f937](https://github.com/this-is-tobi/rta/commit/878f937fb975fd02fbff6cb9ef34e352391d55e6))
* **config:** a config file with nothing in it opens on the starter, as no file does ([9f659e7](https://github.com/this-is-tobi/rta/commit/9f659e78517f5fcdaca567fd36253257a46b9cae))
* **config:** a file rta creates comes with the schema its header names ([3c18dd0](https://github.com/this-is-tobi/rta/commit/3c18dd006eb1f7a0716339775b6ec7d3aa2e7c69))
* **config:** a key that is the end of a dotted one is told the spelling it was meant for ([2432027](https://github.com/this-is-tobi/rta/commit/2432027b350e0d3d52d9553e9e2e3e15ed041af0))
* **config:** a plugins: key is read and written nested, never as a dotted key ([d5011a9](https://github.com/this-is-tobi/rta/commit/d5011a919d22492b7ce060588544f6f790bb6966))
* **config:** a stated key can be found by its line, and a misspelt one names its neighbour ([722e370](https://github.com/this-is-tobi/rta/commit/722e37003c3ea3b7c156030dc23011baa65e9a43))
* **config:** a write keeps the comments, order and unknown keys of the file it changes ([38de0c7](https://github.com/this-is-tobi/rta/commit/38de0c737aaa793b80831520649f8bff1d501069))
* **config:** any interactive command names a config left at the old macOS location ([2538220](https://github.com/this-is-tobi/rta/commit/25382207a0c538a65a73e72884debdc9cf8ce01b))
* **config:** check names the keys of the file rta ignores, with the likely fix ([c897cdb](https://github.com/this-is-tobi/rta/commit/c897cdbd15800cb4b96dc9ef45e836ae6723c823))
* **config:** replace, a commented starter, and a schema modeline in the header ([b2ae357](https://github.com/this-is-tobi/rta/commit/b2ae3576fd74e973ce901e493984ea950fb1fbe4))
* **config:** rta config is the front door to the config file ([778c6f5](https://github.com/this-is-tobi/rta/commit/778c6f59ec5a8145d1bf43be1d1b24f74ed05804))
* **config:** rta config set output says when RTA_OUTPUT outranks the file ([02c5835](https://github.com/this-is-tobi/rta/commit/02c5835fa9b54cc0b960ccec03f29881f9a83263))
* **doctor:** a dashboard tile whose capability no longer exists is a warning ([8871109](https://github.com/this-is-tobi/rta/commit/88711095569af8b80fa632566874073d41044b2b))
* **doctor:** each config key rta does not read is a warning row with its line ([d7fca57](https://github.com/this-is-tobi/rta/commit/d7fca57c21e8e145ad2938d8b75d97979ba2bd30))
* **doctor:** the report reads as a verdict, in the order of what needs you, and gates on its exit ([4113260](https://github.com/this-is-tobi/rta/commit/411326066ada5ec79ffadc2958ba4810cba94d2f))
* **editor:** one place that picks the editor and hands it the terminal ([3e18909](https://github.com/this-is-tobi/rta/commit/3e18909a8e3505a5cb9997e7e9e97eb9dbb5eddb))
* **format:** a window can be written in days ([129fb93](https://github.com/this-is-tobi/rta/commit/129fb93ba537c0751aabf635b85811f3755e01ce))
* **grant:** receipts say what was revoked, how to put it back, and how much was granted ([a54ad0d](https://github.com/this-is-tobi/rta/commit/a54ad0de279c2a83d8dcd64cb9e86d8be75f6921))
* **grant:** the roster is one short line per grant, one table whatever is in force ([caad77b](https://github.com/this-is-tobi/rta/commit/caad77bb8137b0486163953df1fa1e9dc7cf06ab))
* **help:** the first step to connect an agent is rta init, as the quickstart and the README say ([b1123b4](https://github.com/this-is-tobi/rta/commit/b1123b43b12a5ea6acd4be67533de4a3357b9601))
* **http:** a JSON body goes out as JSON, every request names itself, and --data-file ([00453d6](https://github.com/this-is-tobi/rta/commit/00453d62374db78fa183757b7a7ec844dfff36b0))
* **http:** curl's --data [@file](https://github.com/file) is refused at a terminal and points at --data-file ([4a4b661](https://github.com/this-is-tobi/rta/commit/4a4b66157331917bf0ecd6cf7f336f128d2df5ff))
* **init:** a first-run assistant that connects the agent clients and writes no config ([8eff397](https://github.com/this-is-tobi/rta/commit/8eff397c988f9384f85c58ba497079146350d579))
* **init:** offer the first-party plugin index at a terminal instead of printing the command ([1d0ab37](https://github.com/this-is-tobi/rta/commit/1d0ab379eda862c33169a831c9b7d0ebec7aaafb))
* **kv:** ask for the passphrase before the value, and twice when it creates the store ([7bc6de6](https://github.com/this-is-tobi/rta/commit/7bc6de65eba3aea1a68b537d3104044bdd6d31de))
* **kv:** kv get, env, copy and edit declare that they reveal a stored value ([30bf36d](https://github.com/this-is-tobi/rta/commit/30bf36d1699064babaa3038c67bc322ac8e06efa))
* **kv:** name the keys in a folder when one is given for a key, and let kv list take a folder ([419cc50](https://github.com/this-is-tobi/rta/commit/419cc50b7f51958ec60b2ce4672fd6003f57f4a1))
* **lockdown:** a lock can stand on every agent, and a lock window can be in days ([2a8625f](https://github.com/this-is-tobi/rta/commit/2a8625f92e6d455864fc839a8bacb2c30441e092))
* **lock:** lock add --all freezes every agent, and a name nobody used is said so ([7cae53c](https://github.com/this-is-tobi/rta/commit/7cae53cd93e0f1bde2772ba283130fa4a005e96b))
* **match:** name the two floors a hint can trust ([d353145](https://github.com/this-is-tobi/rta/commit/d353145223a68748c11c7aed0c53f087bcf898f6))
* **match:** one word matcher for search, explain and the unknown-command hint ([db25f1b](https://github.com/this-is-tobi/rta/commit/db25f1b027d4a816493f8f86145c2d4f6c7b8205))
* **mcp:** a duration input is published with its pattern and range, and a number needs a unit ([e28bccd](https://github.com/this-is-tobi/rta/commit/e28bccd4a262922dd53fcb147316475bab85813b))
* **mcp:** a tool shows the text a capability wrote for the model in place of its description ([1f6e25a](https://github.com/this-is-tobi/rta/commit/1f6e25a8103d8bb5584723922360d9d36691177c))
* **mcp:** install says where it registered, takes the server's options and re-registers ([5224728](https://github.com/this-is-tobi/rta/commit/5224728a7f50190b34ae83793fb1915f53a96fc9))
* **mcp:** install with no client lists the clients and marks the ones found here ([354bdc7](https://github.com/this-is-tobi/rta/commit/354bdc7148d817e665d8757b88cf1f707dae9d61))
* **net:** the network overview is net.overview, as every other plugin's is named ([2775ee6](https://github.com/this-is-tobi/rta/commit/2775ee6313d5644aea1ddad9b006cb26bdbb9019))
* **note:** done, reopen and rm take several ids, and done on a plain note checks it off ([6105ff6](https://github.com/this-is-tobi/rta/commit/6105ff69a25f2094117b069e1c71d1d329215a05))
* **note:** due dates take the forms people type, and the Due column names the day ([b279406](https://github.com/this-is-tobi/rta/commit/b279406bd25ff7ea98f37c1db724e6bbe08e721b))
* **paths:** keep the config in $XDG_CONFIG_HOME/rta, else ~/.config/rta, on every OS ([1314a4f](https://github.com/this-is-tobi/rta/commit/1314a4f40315868216c1509089e0d5c05cb7594b))
* **plugin:** a capability can declare examples that cannot go stale ([a14ed2a](https://github.com/this-is-tobi/rta/commit/a14ed2a51a56c1d007b951d633aa1aef21227d1d))
* **plugin:** a capability can declare that it reveals what a masked view withholds ([8ed2c6f](https://github.com/this-is-tobi/rta/commit/8ed2c6f186a518e1c18d9ddb57c0f0f6ea577afe))
* **plugin:** a capability can declare the words people search it by ([4b74c61](https://github.com/this-is-tobi/rta/commit/4b74c617db9938895ac073b133dad79d0c83f81a))
* **plugin:** a capability can name the one value that is its whole answer ([e67e5a1](https://github.com/this-is-tobi/rta/commit/e67e5a165d8696ef1663266cf0162c96735bd6f6))
* **plugin:** a capability can write separate text for the agent that reads its tool ([9692434](https://github.com/this-is-tobi/rta/commit/96924346ec1d3057e5ee1ba37bb196aad036da05))
* **plugin:** an input can be a duration written with its unit ([8265601](https://github.com/this-is-tobi/rta/commit/82656014b3763700f10113a0188e524952892321))
* **plugin:** an input can declare a one-letter flag ([5fde061](https://github.com/this-is-tobi/rta/commit/5fde061c4dc8e2ae5ffb68e8ca24a62f1d1fd459))
* **plugin:** an upgrade that newly declares a revealing capability is held back and named ([2c2a562](https://github.com/this-is-tobi/rta/commit/2c2a56220fd917dee4a0e001c601e053771956d2))
* **plugindist:** a capability of a first-party plugin names the plugin ([4050217](https://github.com/this-is-tobi/rta/commit/405021702529c1dee4a5b4e4bdce691c4f639060))
* **plugindist:** the twelve first-party plugin names are known to the binary ([4f8fc2a](https://github.com/this-is-tobi/rta/commit/4f8fc2a1997c6bb3191d2873ae94e6aa19ea845a))
* **pluginhost:** a plugin that cannot start says how to stop launching it ([f4354a6](https://github.com/this-is-tobi/rta/commit/f4354a6018c83e125481c9a1484ca8e9acbe78f2))
* **plugin:** the grammar of a duration is published as a pattern a schema can carry ([be9fef9](https://github.com/this-is-tobi/rta/commit/be9fef9b6cc36635faf31aa442a9813cc5e4441c))
* **profile:** a profile naming a first-party plugin that is not installed names the install ([3064989](https://github.com/this-is-tobi/rta/commit/30649894d027ae0ff83424c36de5346023d06b65))
* **profile:** the environment that is on is named before the commands it acts on ([19213a4](https://github.com/this-is-tobi/rta/commit/19213a4d4b825835c3299845f4cfa113113c4ab2))
* **render:** pretty output is drawn in ASCII under a locale that cannot show a box ([9f006b2](https://github.com/this-is-tobi/rta/commit/9f006b248fd5f6546c344d2c893c15decdc5d3b9))
* **sdktest:** a mask must say where the value can be read, and a reveal must not mask itself ([ed1761f](https://github.com/this-is-tobi/rta/commit/ed1761f9f3fb437544f1e33b8c35ea803402c763))
* **sdktest:** a plugin's agent-facing words and time inputs are held to the catalogue's rules ([3360e61](https://github.com/this-is-tobi/rta/commit/3360e61642ad18b8fcd298e9cc07906573472194))
* **sys:** the processes ps cannot rank are a one-line note, not a warning on every run ([dcd389d](https://github.com/this-is-tobi/rta/commit/dcd389d53a872f915fd6387bab9a9de6fdc3044c))
* **tui:** a call parked for you shows on every screen, and w answers it from anywhere ([af5e7be](https://github.com/this-is-tobi/rta/commit/af5e7be0d2387ba9cf61f4feabba4bd09133e39e))
* **tui:** a form box holds a duration to its grammar and range as it is typed ([d92405a](https://github.com/this-is-tobi/rta/commit/d92405a3606afff270a456cc270cbeeffa8bdab1))
* **tui:** a tile too narrow for a table draws one line per row, and cuts a path in the middle ([dcc3fe6](https://github.com/this-is-tobi/rta/commit/dcc3fe6cc78738340dec97bff8e11b5bd1af3302))
* **tui:** a tile with nothing to say from where rta was started is a muted sentence, not an error ([fc621ab](https://github.com/this-is-tobi/rta/commit/fc621aba00b43bcef15336fa2910cab8a7f6626f))
* **tui:** an action that leads to a read with nothing left to ask runs it ([9508ff3](https://github.com/this-is-tobi/rta/commit/9508ff3ef7e0fe55e0ae2afd27f942dc072940fd))
* **tui:** an agent's call is answered in one key and one line, with the call on screen ([da299f3](https://github.com/this-is-tobi/rta/commit/da299f32b7631620c99f23790119ec9ccc47432c))
* **tui:** an idle search is one line, and two rows of tiles fit a 24-line terminal ([69fa88a](https://github.com/this-is-tobi/rta/commit/69fa88afd2f484e6f74c41e860e4072005fc14a8))
* **tui:** enter runs a read that needs nothing, and ctrl+s submits any form ([8c96280](https://github.com/this-is-tobi/rta/commit/8c9628014b5304d18d4d35ae6efb5debe75a0c92))
* **tui:** the agent tile says how many parked calls are waiting in its own border ([5abdb02](https://github.com/this-is-tobi/rta/commit/5abdb02cb0f9e7cb697ce70bb961fb03cbe110cd))
* **tui:** the catalogue says who needs a grant, and opens on what an agent can reach ([fd3ce2e](https://github.com/this-is-tobi/rta/commit/fd3ce2ec82d428bd2cf1c11ba5715ece7cdcdde7))
* **tui:** the landing dashboard leads with the machine, the agents and the grants ([2c04c9a](https://github.com/this-is-tobi/rta/commit/2c04c9a650df94e51f8c86df4d4d011ae09c51b6))
* **tui:** the plugins pane lists a plugin that was approved and did not start, with its reason ([49dc7b9](https://github.com/this-is-tobi/rta/commit/49dc7b99a041e89a4e2cf67c97608e3cc6e18168))
* **tui:** the search bar and the catalogue filter ask the shared matcher, keywords included ([68d801f](https://github.com/this-is-tobi/rta/commit/68d801fbcf7dee9655bbf22638e90aee9908f059))
* **tui:** the search names the install for a first-party plugin that is not here ([91b6ce4](https://github.com/this-is-tobi/rta/commit/91b6ce47853b3eea91c2043d2361147af86bb6f3))
* **tui:** typing on the landing screen is a search, and the letters belong to a tile ([bc61b6e](https://github.com/this-is-tobi/rta/commit/bc61b6e9dd853f11e8e91069fdc51de04a3894d9))
* **view:** a table's cursor can name the input that takes it ([db1b3a0](https://github.com/this-is-tobi/rta/commit/db1b3a0afe694281944ef4758e576b69859e1ccb))


### Bug Fixes

* **agent:** a bare allow is for the person shown the call, and the card is cleaned ([72a7c5f](https://github.com/this-is-tobi/rta/commit/72a7c5f9f7c175aa379867d4664dd203922e49cd))
* **agent:** a closed input ends the allow question's line ([ce5e1da](https://github.com/this-is-tobi/rta/commit/ce5e1da14adb194e00ba11a0a00d9eb8da85afa3))
* **agent:** an agent's overview names that agent's last call, not anybody's ([f8056c3](https://github.com/this-is-tobi/rta/commit/f8056c3f5213464117442d3763e2f0e9d05dc745))
* **agent:** the session column joins the compact log only when one agent has several servers ([684a4d3](https://github.com/this-is-tobi/rta/commit/684a4d31daf20d7d11e885fcf446d1720303ff0a))
* **agent:** the session column joins the log only when one agent's servers call in turn ([6ab1d44](https://github.com/this-is-tobi/rta/commit/6ab1d447e12439bc42fc3bacaee7057463fe9dc2))
* **app:** `plugin install` answers the name only for a refusal that is about the name ([6312eca](https://github.com/this-is-tobi/rta/commit/6312ecab72195b594ec3cc056b6a448c48c31dde))
* **app:** `plugin rm` removes, and an empty index listing names the index that works ([d6998e3](https://github.com/this-is-tobi/rta/commit/d6998e3aaa14cc2c4181afd1605f935d382536a2))
* **app:** `plugin untrust --all` with one approval speaks of it, not of them ([3940946](https://github.com/this-is-tobi/rta/commit/394094688e9ab8b7426d22e4d070598c41844155))
* **app:** ^C at the confirmation question says no at once, and ^D no longer runs on from it ([d75e383](https://github.com/this-is-tobi/rta/commit/d75e383bc3b81a19e61649f4f01db2f5ee3a7c24))
* **app:** a first-party plugin's name typed as a command or capability says to install it ([91192d1](https://github.com/this-is-tobi/rta/commit/91192d10f29c5dde029f2f5b8633c208a75c34bc))
* **app:** a flag hint offers what the help lists, and never a credential ([1e72e09](https://github.com/this-is-tobi/rta/commit/1e72e095484127af2a7b119c0e527bc5d98cbd78))
* **app:** a human-only verb the deny list names alone gets no alias, whoever declared it ([505d3ae](https://github.com/this-is-tobi/rta/commit/505d3aed31bc5b4084a6a6ddc9815368acd1ba0b))
* **app:** a synonym of a command is not told it might be a service, and uninstall means remove ([aa36d64](https://github.com/this-is-tobi/rta/commit/aa36d64bb09fa6d5b601fa5f69dcfe5c68aee710))
* **app:** a word that reverses a command is not offered it as the closest match ([2360ac4](https://github.com/this-is-tobi/rta/commit/2360ac403938e8eaf0a9f89203f54211c45c0128))
* **app:** an empty word is not a near miss of every command ([f99528a](https://github.com/this-is-tobi/rta/commit/f99528ab2b0b49abf029d13f6a2c60e56122abb3))
* **app:** an error nothing coded is written as prose whatever -o says ([2e65473](https://github.com/this-is-tobi/rta/commit/2e65473d8bd461cb4fde37efd76ea3086bc496f5))
* **app:** completion help carries the recipe that works, and a terminal is not sent the script ([9149e8b](https://github.com/this-is-tobi/rta/commit/9149e8bcebed1d137e710aba3d663a3b15e66f30))
* **app:** help sent to a pipe is laid out at eighty columns, the width a pager has ([5a54438](https://github.com/this-is-tobi/rta/commit/5a54438224d44507a02d00f6eab321ea76e96e03))
* **app:** plugin index list no longer lists --yes and --dry-run ([f02e8a3](https://github.com/this-is-tobi/rta/commit/f02e8a3a78e7fff5a28f475890417cf5d1a1089e))
* **app:** profile set reads a duration as one and says what it takes when it is not ([3931766](https://github.com/this-is-tobi/rta/commit/393176649d51b6ee6e2a7e7d97cf9068e6d77cf0))
* **app:** rta lock refuses a word that reads as a verb, and a flag with no name asks for one ([7b74e11](https://github.com/this-is-tobi/rta/commit/7b74e1163c37fbec6351b250443264ef769758a5))
* **app:** show is never an alias of a get, so no look at an entry hands over its value ([5f06420](https://github.com/this-is-tobi/rta/commit/5f064205d300880d51320a4c0e83d477e0bbda39))
* **app:** the completion scripts still honour --no-descriptions ([1703a7c](https://github.com/this-is-tobi/rta/commit/1703a7c4560c65d84270ef1462815182e7092022))
* **app:** the kv init example names a recipient that works as typed ([e773b24](https://github.com/this-is-tobi/rta/commit/e773b2408783bdce6e8a35131b0a7ead31c6405e))
* **app:** the untrust receipt says what is left, not a bare count ([cca57b5](https://github.com/this-is-tobi/rta/commit/cca57b53de6c0d8d58982fc0f4b3437f05c073fb))
* **config:** `plugins` is named as a block, and an unset block says so ([42ed906](https://github.com/this-is-tobi/rta/commit/42ed906d049254d537f84e57e1ee90e9057e6c49))
* **config:** a byte-order mark is not part of the file's first key ([2b8b44b](https://github.com/this-is-tobi/rta/commit/2b8b44b9f220529c7a71e863690319c2f07432fd))
* **config:** a file that does not parse is no longer told to be re-created with rta init ([236da5e](https://github.com/this-is-tobi/rta/commit/236da5e298f9d7ddcb1d6d8bb71e103da5b3e7a8))
* **config:** a file that does not parse points at rta config edit, and two lint notes say why ([1cedb1a](https://github.com/this-is-tobi/rta/commit/1cedb1a92036359bb6ef6bf0edb131c42a11da8a))
* **config:** a link in the working-directory fallback is not followed on a write ([10f0fc5](https://github.com/this-is-tobi/rta/commit/10f0fc57a0226576ce7c0ea7d7560028f1387e7a))
* **config:** a write keeps the line endings of a file that uses CRLF ([2a47d42](https://github.com/this-is-tobi/rta/commit/2a47d426b5d43f0723177d82ee6ff819486a9564))
* **config:** a write to a config that is a symlink changes the file it points to ([d645bd1](https://github.com/this-is-tobi/rta/commit/d645bd15a5c55bed17c41a6453434848a7aa335a))
* **config:** rta config get withholds the credentials rta config show withholds ([c43c5ee](https://github.com/this-is-tobi/rta/commit/c43c5eea0f77058710bef6f75ee8c2ee39691a56))
* **config:** rta config names $XDG_CONFIG_HOME as the source for any absolute value of it ([a0c5427](https://github.com/this-is-tobi/rta/commit/a0c5427a8310b82ae91f0cd8d0a70de9ea945079))
* **doctor:** a trusted plugin that failed to start has a row in the report ([4180d05](https://github.com/this-is-tobi/rta/commit/4180d05676361f0674dbd0ccadbff3d146d4269b))
* **doctor:** an empty kv store row sends a person where every kv hint does ([5b5ec90](https://github.com/this-is-tobi/rta/commit/5b5ec902bd19940dcc13d36ea78ca1becc183e5e))
* **doctor:** the locks row names a lock on every agent as such ([16af039](https://github.com/this-is-tobi/rta/commit/16af0395ea6442d8bda655d32a204d64a85331fc))
* **doctor:** the verdict is drawn by the renderer, so it fits the screen and obeys NO_COLOR ([9f15428](https://github.com/this-is-tobi/rta/commit/9f15428dc72fc17da3a6423265db978c66d644d4))
* **explain:** the suggestions stop where the likeness does ([1eafc3c](https://github.com/this-is-tobi/rta/commit/1eafc3cb7c499e31add6ae32083820c1c6bb882c))
* **format:** a window of days plus hours that overflows is refused, not wrapped ([aa7fc5e](https://github.com/this-is-tobi/rta/commit/aa7fc5efcb5ce17766699193a8a29585bff2697f))
* **git:** the detail page outside a repository says why, and the quiet tile's enter shows it ([3b59afa](https://github.com/this-is-tobi/rta/commit/3b59afa71706f1ec1898634f6eac0705f58aed9c))
* **grant:** an unknown first-party plugin target says to install it, and no other word is guessed ([a1d4d7e](https://github.com/this-is-tobi/rta/commit/a1d4d7ed003287a95cce08116802268630617ca7))
* **grant:** the breadth note names a fourth destructive capability instead of counting it ([d334db5](https://github.com/this-is-tobi/rta/commit/d334db57fb0e651a2a515e226b539fa57135ebde))
* **http:** --local-network reaches localhost and ::1, over http when the host is one's own ([91e4688](https://github.com/this-is-tobi/rta/commit/91e46889e725a48c3b00f71a35c741135007c139))
* **http:** a dash named as a body or credential file says a pipe is /dev/stdin ([b58209f](https://github.com/this-is-tobi/rta/commit/b58209f5552cc01fabd524f44931f11381b42783))
* **http:** a URL without a scheme is plain http only for localhost and addresses ([0c2bf5f](https://github.com/this-is-tobi/rta/commit/0c2bf5f77f51d6fb24c4ef4e88bc1925f2d90806))
* **init:** a machine with none of the clients it knows is told so ([1003183](https://github.com/this-is-tobi/rta/commit/1003183e8e5e59db55cab35765a4b3e7bc3127a6))
* **init:** a terminal that cannot redraw is shown what yes does before it is asked ([82b47a9](https://github.com/this-is-tobi/rta/commit/82b47a90386fc951b94f494fcbf7a53265dcefa2))
* **kv:** a re-key with no store offers the key-file store beside the passphrase one ([d5a69bf](https://github.com/this-is-tobi/rta/commit/d5a69bf63195c209263d0bb436505fa010484bd5))
* **kv:** an empty first answer for a new passphrase ends the prompt instead of asking again ([4a5c2df](https://github.com/this-is-tobi/rta/commit/4a5c2dfc1d0203dea3a567b2ab4a7f9810ee95da))
* **kv:** every hint for a missing store gives the one next step that makes it ([5594961](https://github.com/this-is-tobi/rta/commit/559496149338d10efe510e17cd1fdeaec7393431))
* **kv:** only kv.set's passphrase help speaks of a store that is not made yet ([19e3570](https://github.com/this-is-tobi/rta/commit/19e3570580b60057d9ba16f1091d49008c4c8bdc))
* **kv:** the passphrase box says that a store not made yet is locked with what is typed ([13b5799](https://github.com/this-is-tobi/rta/commit/13b5799b865f98d1bb0525e8919a07fd350c5cf8))
* **lock:** lifting the row on every agent says what was not there, and completion skips it ([a9bf1ac](https://github.com/this-is-tobi/rta/commit/a9bf1ac8ad66482fc1ec9a40c581af2395ff175b))
* **match:** a tail that is not a plural makes another word, so backup is not a slip of back ([3c96ab2](https://github.com/this-is-tobi/rta/commit/3c96ab2797f1cc9c51157849aa3df2eaa707ea81))
* **match:** a word that only begins like a name is not taken for a typo of it ([e434170](https://github.com/this-is-tobi/rta/commit/e434170334f2fea0a54f030ce52648322dfd131a))
* **mcp:** a call nobody answered is refused as unanswered, not as ungranted ([4bf669e](https://github.com/this-is-tobi/rta/commit/4bf669efd06afde8ec4b10aa988b3adedfb7a8e9))
* **mcp:** a grant past its remembered deadline is said to have ended, not to have expired ([1b8ea23](https://github.com/this-is-tobi/rta/commit/1b8ea234c2169c0929475d69c83838b8cd22fbf3))
* **mcp:** a refusal says when the grant the agent was running on has ended ([86bb037](https://github.com/this-is-tobi/rta/commit/86bb037dccc9263641eec2a199aa018bac9ea4bc))
* **mcp:** a wait that ended before its deadline is not reported as an unanswered question ([4dab374](https://github.com/this-is-tobi/rta/commit/4dab374cf643e74d21ba5640c1b2de90487b87b8))
* **mcp:** an unanswered call is never told the operator had zero seconds ([05e71d3](https://github.com/this-is-tobi/rta/commit/05e71d3e28eea3b40c391c9b347ba4ac0a572867))
* **mcp:** install does not drop the variables an operator set on a registration ([6c5008c](https://github.com/this-is-tobi/rta/commit/6c5008c434b8bf1a886f239c24d2d7aff4fe68f6))
* **mcp:** install replaces only a registration that starts rta's server ([477aeb8](https://github.com/this-is-tobi/rta/commit/477aeb8d394529341b0c61c43d26acab17a8bfef))
* **mcp:** the doorbell names the command that answers and carries no request id ([5aa1f17](https://github.com/this-is-tobi/rta/commit/5aa1f171f4c1125f4996f2c008c4b8a1a7b4a390))
* **mcp:** the record says where an approval was given instead of approved by form ([3719b81](https://github.com/this-is-tobi/rta/commit/3719b81ce54547a9318182b93b5b8244a315fe16))
* **note:** an empty list on the TUI says which key adds a note, not a capability's name ([cab3b94](https://github.com/this-is-tobi/rta/commit/cab3b9443e55bf2f87fe95f1b40aff021c3c902e))
* **note:** ids joined into one argument are told to be separate, with the call that does it ([794a7df](https://github.com/this-is-tobi/rta/commit/794a7dfd377c7183b177fa1e43ba3fdf36f2c981))
* **note:** the due-date forms fit the two lines a form gives a field's help ([c46c890](https://github.com/this-is-tobi/rta/commit/c46c890df94672b1ecd63dd535e531de37defa02))
* **plugin:** -v is not a flag a capability command has, so an input may take it ([3bdf935](https://github.com/this-is-tobi/rta/commit/3bdf935449a32aeada7c4a7183099aa5c7cc4a79))
* **plugin:** an example's text values are held to the rules a default's are ([eac7f49](https://github.com/this-is-tobi/rta/commit/eac7f4946914af4c99e555db011a578d7e554b0d))
* **plugin:** an out-of-range duration is not sent to a schema range that does not exist ([cf14e9c](https://github.com/this-is-tobi/rta/commit/cf14e9cf46794a54a70f0406bb2bcfa68737d695))
* **pluginconf:** a dotted key written flat is told to nest, not that it has no value ([f743f31](https://github.com/this-is-tobi/rta/commit/f743f31c2fdf333a1c2bc6203b0b8abc27cbebf4))
* **pluginconf:** a duration key shared by several capabilities keeps the widest of their ranges ([88b2bf9](https://github.com/this-is-tobi/rta/commit/88b2bf9e3f84d06ec79cf8eaf78c514e4caa4a4c))
* **plugindist:** a first-party plugin that is running is never told to install itself ([5b90443](https://github.com/this-is-tobi/rta/commit/5b904431c4e8228bb336b0743510369eca57de72))
* **pluginhost:** a launch the machine refused is listed with its own way out ([689ad81](https://github.com/this-is-tobi/rta/commit/689ad8189b20da4b94910f9d8c2a86d70c1fed35))
* **plugin:** text that is no duration is refused as what it is, not as text ([c06de42](https://github.com/this-is-tobi/rta/commit/c06de42a145181fdf17e88bf54fbb3a956f33468))
* **plugin:** the digits a flag hands over for a duration are named as a bare number ([87ab1c3](https://github.com/this-is-tobi/rta/commit/87ab1c36a2fe5315669e4f4d18ee5dd7a5806e21))
* **plugin:** the refusal of a reveal with no gate says what to declare and stops there ([56293ed](https://github.com/this-is-tobi/rta/commit/56293edec4083255ae912eb904fe292b39ca0bd0))
* **profile:** the badge follows what the terminal can show, as the result under it does ([73a8007](https://github.com/this-is-tobi/rta/commit/73a80072ffbffd6296e4c74127fb33ea797baf03))
* **profile:** use --for takes a day like every other window ([ab12437](https://github.com/this-is-tobi/rta/commit/ab12437f7fb2d8b6a8f3ebe963adb17f05f11517))
* **rta:** hand the plugins that failed to start to the commands that list plugins ([7af4629](https://github.com/this-is-tobi/rta/commit/7af46299d74213a52e3fc5ed3829e181fff36c48))
* **sys:** the host page and the overview's host line no longer fail over an id they never show ([12994c2](https://github.com/this-is-tobi/rta/commit/12994c266374a9f21170ac029d165108c04b2a80))
* **sys:** the note about processes ps cannot rank reads as a sentence ([4bce568](https://github.com/this-is-tobi/rta/commit/4bce568c091cb781bda156adb6572e600fd9d26a))
* **theme:** an overdue note is drawn as an error instead of with no colour at all ([df51b78](https://github.com/this-is-tobi/rta/commit/df51b785082855405647a5d915a0f96dc0327bfd))
* **tui:** a glanced table is cleaned of terminal escapes before its lines are built ([998ed39](https://github.com/this-is-tobi/rta/commit/998ed39fffa0abbde7cfa58e0a3977b1e43f24ff))
* **tui:** a glanced table keeps a cell with a line break on its own row ([a3211e0](https://github.com/this-is-tobi/rta/commit/a3211e0e5d1d23af95230a0b8847567a224e2687))
* **tui:** a glanced table keeps the columns that grade or date a row, and drops notes first ([d08a7e8](https://github.com/this-is-tobi/rta/commit/d08a7e8839a3cdec37f98d9ac8f3d2440c77c41a))
* **tui:** a note's page opens done, re-open and remove with its id as text ([9416e73](https://github.com/this-is-tobi/rta/commit/9416e736beae3171db79a6304363dc3cbcba711d))
* **tui:** a paste on the landing screen is a search ([1260d13](https://github.com/this-is-tobi/rta/commit/1260d13fd0b74bd39b089ab5d3427b32326ddd51))
* **tui:** enter allows only the call the line named, and says when it is gone or changed ([a0f85ca](https://github.com/this-is-tobi/rta/commit/a0f85cab80411124d89016e34e6a600773942689))
* **tui:** the plugin config form reads and writes a dotted key nested, as the runtime reads it ([43e4cf7](https://github.com/this-is-tobi/rta/commit/43e4cf7a62976e979e3121b2a72ae0aebc5f4d56))
* **tui:** the queue leaves w to a tile that declares it for something else ([bd234f7](https://github.com/this-is-tobi/rta/commit/bd234f7fc1c10547fa4e47dfdd98ddb63f2a3e40))


### Code Refactoring

* **app:** the client lookup answers found-or-not and the client scan sizes its list ([331378d](https://github.com/this-is-tobi/rta/commit/331378d703011288be813f68f619869de56e4700))
* **config:** parse reads configuration text the way the loader reads the file ([ffb369a](https://github.com/this-is-tobi/rta/commit/ffb369abe86c3ee463b90d2342d6c7c744653f42))
* **doctor:** drop the verdict's status, which nothing reads since the renderer draws the line ([b3fbda1](https://github.com/this-is-tobi/rta/commit/b3fbda1b8cb608fd7d932cc07e13ec87ce55876b))
* **explain:** did-you-mean asks the shared matcher ([cfe010a](https://github.com/this-is-tobi/rta/commit/cfe010af5d6e60d7602f3a512b496630cbb8717f))
* **http:** whether an [@name](https://github.com/name) is a file here is a question of its own ([83f2074](https://github.com/this-is-tobi/rta/commit/83f207457658b7a04977c9a5620a5f1af310cf6e))
* **init:** the command no longer takes a registry it does not read ([b447ebd](https://github.com/this-is-tobi/rta/commit/b447ebdae2075f180291e6116064b75b39de4fdb))
* **kv:** say why a passphrase prompt that ends unanswered is no passphrase ([e73b725](https://github.com/this-is-tobi/rta/commit/e73b7259637ba28f13f7e22afc16351eb1bcc4bb))
* **mcp:** the profile argument is explained once at the handshake, not in every tool ([c539a3d](https://github.com/this-is-tobi/rta/commit/c539a3d3b2545fb74a2889cd6245d4d1337cc1d6))
* one edit distance for every did-you-mean ([69bd930](https://github.com/this-is-tobi/rta/commit/69bd9305e3770d6c6eee26624578b048945c0914))
* **plugin:** clear the linter's findings in the duration, reveal and wording code ([f01259c](https://github.com/this-is-tobi/rta/commit/f01259c227729739c50e320f790ef1b1e5969725))
* **plugin:** remove answers to rm through the verb rule, not a hand-added alias ([95263e7](https://github.com/this-is-tobi/rta/commit/95263e707a65b40cee5b29fe41118d07fd7ac243))
* **tui:** the ranked search results are sized once instead of grown ([5c5f762](https://github.com/this-is-tobi/rta/commit/5c5f762afa2b5d68c5dce891d1ec214331dce263))
* **tui:** the search bar and the catalogue filter ask one ranking function ([cfe81a1](https://github.com/this-is-tobi/rta/commit/cfe81a1a1b95b12114e42f7f572cda6399a7278a))

## [0.35.0](https://github.com/this-is-tobi/rta/compare/v0.34.0...v0.35.0) (2026-10-04)


### ⚠ BREAKING CHANGES

* **host:** rta is no longer supported on Windows.

### Code Refactoring

* **app:** doctor, plugin dev, scaffold and the app's tests stop making room for Windows ([453db0b](https://github.com/this-is-tobi/rta/commit/453db0b5682d90080ec5c8cbb90edf25d91eed70))
* **app:** the client scan stops matching rta.exe, and a skip no platform reaches goes ([cf1e282](https://github.com/this-is-tobi/rta/commit/cf1e2826b3aa5c53de7f90b494081d80fca749c9))
* **atomicfile:** rename and open once, with no wait for a refusal that clears ([710139c](https://github.com/this-is-tobi/rta/commit/710139c871e25204223533350b03a342578686b6))
* **audit:** drop the Windows client paths and the %VAR% reference form ([324cd45](https://github.com/this-is-tobi/rta/commit/324cd45ce1060b0c9eb35f6363b367e10600bce6))
* **builtin:** drop the Windows skips, build tags and wording from the other packages ([5fc668c](https://github.com/this-is-tobi/rta/commit/5fc668c010ef755094e4f2509f1ff33d6bba6a29))
* **builtin:** fail where a pipe or a link cannot be made, and name only the slash ([587d4ac](https://github.com/this-is-tobi/rta/commit/587d4ac436b5b4c407829ca16714bbc516c01640))
* **clipboard:** put a program in its own process group and nothing else ([05c1bb3](https://github.com/this-is-tobi/rta/commit/05c1bb3543c0f68ec5bf80cfa61f8f6feae6342b))
* **filelock:** remove and break a stale lock once, with no wait for a sharing refusal ([38063a8](https://github.com/this-is-tobi/rta/commit/38063a839c05169bafabbda3a9ca2146a68eb0d4))
* **fs:** fold the platform split into stat.go and drop the build tags ([cc5683f](https://github.com/this-is-tobi/rta/commit/cc5683f828c1d279a0148990df39dd46521cc6cb))
* **git:** drop the non-unix fallbacks and the volume handling ([f1f999e](https://github.com/this-is-tobi/rta/commit/f1f999ed739cdcd7916360343ead57bc233ae8c0))
* **git:** make the named pipes and links the tests need a failure, not a skip ([2f43a54](https://github.com/this-is-tobi/rta/commit/2f43a54071a3167675d5bcff74e798f387f6db41))
* **host:** signals, process groups, forwards and terminals keep only their unix half ([e378b25](https://github.com/this-is-tobi/rta/commit/e378b25470ce94fda28da408243a482e6eab8b3f))
* **keys:** grade key file permissions without a platform carve-out ([19baa31](https://github.com/this-is-tobi/rta/commit/19baa31f519e89f250d49999fe6a5319dc36aacc))
* **kv:** drop the notepad fallback and the Windows test skips ([19ff8b7](https://github.com/this-is-tobi/rta/commit/19ff8b7b1c58600cc4737118a0f68c0faead0194))
* **kv:** drop the platform that cannot read on from the multi-line paste comment ([670c688](https://github.com/this-is-tobi/rta/commit/670c688de88c9adab6dd0b20bd6f35ea284a8fb0))
* **lockdown:** the recovery hint, two tests and three package docs name no Windows ([462e7d1](https://github.com/this-is-tobi/rta/commit/462e7d1d28687a45b6c08997275cc6cfc37a9f62))
* **mcp:** the token file, the roster and the remotes list are mode-checked unconditionally ([27700e2](https://github.com/this-is-tobi/rta/commit/27700e20b98514bfd7ed95834c065784b737f124))
* **notify:** pick the notifier by darwin against everything else ([99dd048](https://github.com/this-is-tobi/rta/commit/99dd04836f006c2e306a7e67489bad01063b97d7))
* **pathguard:** judge paths as a unix path alone, with no volumes, drives or shares ([2f195f0](https://github.com/this-is-tobi/rta/commit/2f195f08a9d57280c1a2b28e84a735e909695fd5))
* **pkg:** drop the Windows refusal and the unknown-OS fallback ([52fad77](https://github.com/this-is-tobi/rta/commit/52fad77e45e6f979652f942277363ce916d1aef5))
* **plugindist:** place an artifact with one rename and one symlink ([d3138e0](https://github.com/this-is-tobi/rta/commit/d3138e04bc0f5ba329daa524c57744bd57c8ceb6))
* **pluginhost:** one launch path for Linux and macOS ([f530c05](https://github.com/this-is-tobi/rta/commit/f530c05b04c45e1c202c87c13dcf99fd01cf1f20))
* **render:** complete a typed path at the platform separator alone ([cac9834](https://github.com/this-is-tobi/rta/commit/cac98344505d5986515dedc56822cbbf996c6c3d))
* **sdk:** read dials, certificates and ~ paths as Linux and macOS give them ([8c9323d](https://github.com/this-is-tobi/rta/commit/8c9323da458fdd0c2c24de923aa84fcb88444943))
* **sdk:** rewrap the UseVerifierSystem comment ([3a75720](https://github.com/this-is-tobi/rta/commit/3a75720bb59d5a72d0cee818b9ba0a293303603a))
* **stdio:** drop the comment clauses for a system whose terminal rta cannot set ([b843f6c](https://github.com/this-is-tobi/rta/commit/b843f6c50d090cffc3c25e9a9d71eb969e0d2f43))
* **stdio:** read a secret through the terminal's own settings on every platform ([cd28e4b](https://github.com/this-is-tobi/rta/commit/cd28e4be4885c740d68b8700dfc99d82675938de))
* **textclean:** hold a tracked file's carriage return to the same rule as a bare one ([cf5b63b](https://github.com/this-is-tobi/rta/commit/cf5b63bc8c357cf1ab8d05dc27393692a784e559))
* two comments stop describing a platform rta no longer runs on ([970c1fc](https://github.com/this-is-tobi/rta/commit/970c1fcc8d33561831e90335d4359da1df06954a))


### Dependencies

* rta is no longer built, tested or released for Windows ([7756566](https://github.com/this-is-tobi/rta/commit/7756566b07573e13eecfec5ccff97247a49db04b))
* **rta:** the binary builds only on unix, so a Windows build stops at once ([494c2ee](https://github.com/this-is-tobi/rta/commit/494c2ee82fffc42f9511a0d210622624e14c3a7c))
* **rta:** the binary names the two systems it builds on, so any other build stops at once ([f93e353](https://github.com/this-is-tobi/rta/commit/f93e35375c21bd2b843f9bd5995894e8493228e6))

## [0.34.0](https://github.com/this-is-tobi/rta/compare/v0.33.0...v0.34.0) (2026-10-04)


### ⚠ BREAKING CHANGES

* **mcp:** a request over 4 MiB sent to rta mcp serve over stdio ends the session instead of being handled, as it is refused over HTTP.
* **profile:** a profile.json written by an earlier rta carries no seal and is held shut until rta use <profile> or rta use --off writes it again.
* **mcp:** a result larger than 8 MiB over MCP is refused as core.result.toolarge instead of being returned. Start the server with --max-result <MiB> (up to 256) to allow larger ones.
* **eol:** eol.check without a cycle no longer lists every ended cycle. A pipeline that read the whole table passes `--all` (`all` over MCP).
* **fs:** the sizes and shares fs.usage reports, and the largest files of fs.tree's detail page, are disk use rather than length; pass apparent for the old figures.
* **git:** git.remotes has a Use column after URL, so Branches and Origin move one place to the right in the table, its CSV and its JSON rows.
* **fs:** fs.hash with expect fails with fs.hash.mismatch where it returned a match row reading NO; a match still returns the hash and a match row of yes.
* **gen:** a base64url token no longer ends in = padding; base64 keeps it.
* **cert:** the serial row of cert.inspect is hexadecimal where it was decimal; anything comparing it with a decimal serial has to compare the hex.

### Features

* **cert:** expiry at a terminal also takes certificate files, never over MCP ([391c34b](https://github.com/this-is-tobi/rta/commit/391c34b6f45a6bff37f18580e40c96654283e6e5))
* **eol:** check lists the supported cycles and the latest ended, `all` for every one ([41ecc2a](https://github.com/this-is-tobi/rta/commit/41ecc2a7821e1825cb106f2f1e4a13860a83a45d))
* **eol:** eol products lists the first 50 products and says how many there are ([a62253e](https://github.com/this-is-tobi/rta/commit/a62253e7e27b6ed02f4026a2eca45515ac7647cf))
* **git:** branches marks a branch checked out in another worktree ([068b114](https://github.com/this-is-tobi/rta/commit/068b1142a61431e418e09c4195a3eddbedbe7976))
* **git:** git blame shows a window of the file's lines, not only all of them ([92f6a2d](https://github.com/this-is-tobi/rta/commit/92f6a2d951b2a08229e3d59745b4d2cce079c5af))
* **git:** status pairs a staged move into one rename row, as git does ([a421099](https://github.com/this-is-tobi/rta/commit/a4210998e0509fcf3ed957752f91ebcf3c27692b))
* **http:** a bearer token or basic credential can come from a file or a pipe, not argv ([2c0becd](https://github.com/this-is-tobi/rta/commit/2c0becdd6c360e48e325b94c3f58127ff8e443f5))
* **http:** a person at the terminal may ask to reach a loopback or private address ([94fc905](https://github.com/this-is-tobi/rta/commit/94fc9059d2a150936dd96e5033960979f2cc593d))
* **mcp:** a result over 8 MiB is refused while it is received, with --max-result to raise it ([67fd828](https://github.com/this-is-tobi/rta/commit/67fd828d3136cee7e5d94930037e82e23bb070b6))
* **mcp:** a result too large for a model is cut and says so, or is refused with what to narrow ([ba78e6b](https://github.com/this-is-tobi/rta/commit/ba78e6b59953b8c7d70186746e0c017d718d2453))
* **mcp:** what is true of every tool is said once at the handshake, not in each description ([24f2cb8](https://github.com/this-is-tobi/rta/commit/24f2cb8d40938bcefdb420ccf01387f2ecf9898a))
* **sdk:** plugin.ForwardNameRefusal words the refusal seven plugins copied by hand ([63861f9](https://github.com/this-is-tobi/rta/commit/63861f9a10aa558e3627107747e92e98257a77d4))
* **sdk:** plugin.ListedName shows a listed name escaped where it does not read as itself ([0575037](https://github.com/this-is-tobi/rta/commit/057503777e21e4451d22584179334aac8ab4c31a))
* **sdk:** sdktest injects the system's verdict so CertRevoked and CertPolicyHint run on Linux CI ([3f8b7f3](https://github.com/this-is-tobi/rta/commit/3f8b7f3c2b5ffa5a7b84430b678a572de4d45bd0))


### Bug Fixes

* **agent:** an empty log says why on a screen instead of drawing twelve column headings ([73fbfa0](https://github.com/this-is-tobi/rta/commit/73fbfa0707d40ad1c57e7de89bdd4c3825407f46))
* **agent:** log --since refuses a clock reading that never happened and names one shown twice ([5cc52ee](https://github.com/this-is-tobi/rta/commit/5cc52eebcc065b313c544b3ae443bbd9cb0b8c17))
* **agent:** the overview names the command that answers a parked call, not a key ([3ffa69a](https://github.com/this-is-tobi/rta/commit/3ffa69a6a80771ef6daed9f236636e2539290914))
* **app:** a --for that rta use would drop is refused instead ([04510b8](https://github.com/this-is-tobi/rta/commit/04510b8f428be0fe022c156df3dcd9818b42ffea))
* **app:** a flag value the parser refuses is refused as an input check refuses it ([3e6d9ee](https://github.com/this-is-tobi/rta/commit/3e6d9eef106b57a63235570c38ea56efe8ccf3ce))
* **app:** a hand-written command names the argument it is missing or was given too many of ([d7c0b25](https://github.com/this-is-tobi/rta/commit/d7c0b2530cde4330bc6afa39946e6478379502c9))
* **app:** a list flag takes an argument that is not valid CSV as one value ([e574fc5](https://github.com/this-is-tobi/rta/commit/e574fc5201dbc83b496168191f883e6f7f31a014))
* **app:** a value that starts with a dash and a digit is told to follow a double dash ([80aaf9c](https://github.com/this-is-tobi/rta/commit/80aaf9c3a1b9cdbe64f33c368fbce8e8831c2dfb))
* **app:** a word at the root that is not a command says where it may live ([a108878](https://github.com/this-is-tobi/rta/commit/a1088780cdab2d8a3b514d37b4c47edac957ff06))
* **app:** a word naming a plugin waiting for approval says to trust it, not to install it ([dcb21ca](https://github.com/this-is-tobi/rta/commit/dcb21cab72f707c5d16a4ebaec6b0e9b57eecc7e))
* **app:** bare rta on a TERM=dumb terminal prints help instead of opening the TUI ([96b3cc1](https://github.com/this-is-tobi/rta/commit/96b3cc1cdf7e9f8273536bdcddbb2024f2c71649))
* **app:** bare rta over a config that does not parse does not print the parse error twice ([df994ea](https://github.com/this-is-tobi/rta/commit/df994ea4e8a90f591570ee3e97c17f8d1a1d15f0))
* **app:** explain lists what is under a plugin or an ID prefix instead of refusing it ([f9bc105](https://github.com/this-is-tobi/rta/commit/f9bc105a0ce66c1d3340d48fd613741c7947ccce))
* **app:** help for a command that does not exist is a usage error, not the root's help ([f278117](https://github.com/this-is-tobi/rta/commit/f2781172c82cd82ef2d9e20a9476b805caaea0a0))
* **app:** help joins hand-wrapped paragraphs so the renderer wraps them to the terminal ([278812e](https://github.com/this-is-tobi/rta/commit/278812ef1db92da44c49075b2e6b8c7268010169))
* **app:** the note under an install block says what the agent name is for, not an impossible state ([7462b52](https://github.com/this-is-tobi/rta/commit/7462b529f8e0c16f905f40ed825bd82cb784b23b))
* **atomicfile:** a named pipe where a state file goes is refused, not waited on for a writer ([7619d67](https://github.com/this-is-tobi/rta/commit/7619d67c1a216bd2f3a54ed11821effcb7ade5ac))
* **atomicfile:** a replace onto a directory fails at once instead of waiting out every retry ([4fb2e70](https://github.com/this-is-tobi/rta/commit/4fb2e70084ab35b6d48628450459f6f2cc0ad570))
* **atomicfile:** the record, the stores and the shortlists are opened as regular files too ([8eb5c08](https://github.com/this-is-tobi/rta/commit/8eb5c08c8335d6da3be864cc59d99926cc35ad16))
* **audit:** a kube audit against a cluster that did not answer says so, not kubectl's log line ([f11ae93](https://github.com/this-is-tobi/rta/commit/f11ae93da6e20dde1b72d95756c6f0ab64606c6e))
* **audit:** audit and net text fits the budget, and the conventions hold with no exceptions ([ce012b0](https://github.com/this-is-tobi/rta/commit/ce012b0bbd3783710eeb5114499d6cc14d97c000))
* **audit:** deps names the requirements it did not check instead of dropping them in silence ([09945eb](https://github.com/this-is-tobi/rta/commit/09945ebd5c9803deffafb0a79b73910cc3a14772))
* **audit:** deps reads a wildcard pin as a range and === as the pin it is ([6fc2969](https://github.com/this-is-tobi/rta/commit/6fc2969438a8ba64049a3356757b7ffdf455271c))
* **audit:** mail does not say a domain with no MX records receives no mail ([7317676](https://github.com/this-is-tobi/rta/commit/731767632c1d8dca29d689d22b895e4340dd2e28))
* **audit:** mail does not warn a domain with a null MX about MTA-STS ([1b5ae6f](https://github.com/this-is-tobi/rta/commit/1b5ae6f29607712a89c3c91661437bd1934e52d0))
* **audit:** web fails a certificate that is not valid yet instead of grading it ok ([7df16ce](https://github.com/this-is-tobi/rta/commit/7df16ce050f65b3d56453460e892552255dce272))
* **cert:** a certificate that is not valid yet is said to be, not graded ok ([728eeb3](https://github.com/this-is-tobi/rta/commit/728eeb3cfcc6dc87ae217febde85eb766821ef6b))
* **cert:** a certificate with no common name is named in a chain by what it does have ([1c4db24](https://github.com/this-is-tobi/rta/commit/1c4db247d701e73a27bb769886fb15bff6ed044e))
* **cert:** a DER certificate file is read, not refused as holding no certificate ([3e0243e](https://github.com/this-is-tobi/rta/commit/3e0243e90080d58991f5f17d92d52dd89b32098b))
* **cert:** a hint to cert.expiry names the tool over MCP and the command at a terminal ([fa84777](https://github.com/this-is-tobi/rta/commit/fa84777999c950d8b8133ea350b89b87c844ae26))
* **cert:** a mistyped certificate path says there is no such file ([f3e85d7](https://github.com/this-is-tobi/rta/commit/f3e85d7ad29f9be5d49bfc4d4db558a39c80b7dd))
* **cert:** a path that names no file is refused as a path, not dialled as a host ([b83b6bf](https://github.com/this-is-tobi/rta/commit/b83b6bfd26fbf2ceb67968a120ac84656b77c2e0))
* **cert:** an https:// URL is the host it names, as pasted from a browser ([181b399](https://github.com/this-is-tobi/rta/commit/181b399b7974fe745c2542d7c568a1d4b1a1da28))
* **cert:** cert_inspect, cert_chain and cert_pem say first-hand that over MCP they read a file ([5d4fec6](https://github.com/this-is-tobi/rta/commit/5d4fec63395dad04665575ddfb320ae3c3659201))
* **cert:** expiry grades the chain, so an intermediate ending first decides the row ([daebcd8](https://github.com/this-is-tobi/rta/commit/daebcd882c113696dc4feb115955e05f805e9174))
* **cert:** expiry says what it does, not that its reasoning is net.probe's ([aa722d2](https://github.com/this-is-tobi/rta/commit/aa722d2ca1522e1ea1d2cbf553275e45ab182c9e))
* **cert:** inspect lists the IP, email and URI names a certificate answers to ([326dfdd](https://github.com/this-is-tobi/rta/commit/326dfddd1496182764668a1c1c31acd27589dbaf))
* **cert:** inspect names the key a certificate certifies, by type and size ([755eb33](https://github.com/this-is-tobi/rta/commit/755eb335d5b62920385249eee849572dbba9ec29))
* **cert:** inspect says what a certificate is for: CA or not, key usage, extended key usage ([be68865](https://github.com/this-is-tobi/rta/commit/be68865b506b3c836702d79fe3ed49292635f0f3))
* **cert:** inspect shows the serial in hex, as every tool that lists one spells it ([628f6f9](https://github.com/this-is-tobi/rta/commit/628f6f9629bdea8c29449ff7ce0d27ab6491b080))
* **cert:** over MCP a certificate file the server cannot open is not said to be missing ([a37fc67](https://github.com/this-is-tobi/rta/commit/a37fc670b72eafd978876ccf186311ff7e4e6cfc))
* **cert:** over MCP the cert tools say they read a file, and cert.tls says it always refuses ([5d6b0b9](https://github.com/this-is-tobi/rta/commit/5d6b0b9ff8f9afa7378f478dcbf13a64fbe47fd7))
* **cert:** pem --out naming a directory says it is one, not the rename it failed on ([b8c0b24](https://github.com/this-is-tobi/rta/commit/b8c0b245e38d3f3b1f6c9c43a8b5087b42cef981))
* **cert:** the target's help says a host is dialled from a terminal only ([3f7fbea](https://github.com/this-is-tobi/rta/commit/3f7fbeaf8db04d6df4c804e1095cc7c3841cbbfd))
* **cert:** time left is counted down to the second, not stopped at the hour ([dcaeef4](https://github.com/this-is-tobi/rta/commit/dcaeef479ff2f226d1ab6bcb53faa128b09f5723))
* **cli:** a flag the command line cannot leave out is drawn as required, not as optional ([bae3c06](https://github.com/this-is-tobi/rta/commit/bae3c066513356cf33c7a8412d77867561151413))
* **cli:** a flag the environment fills is drawn as optional, not as required ([a016292](https://github.com/this-is-tobi/rta/commit/a01629281066da4d30fa5b42dc4334831c54dbf0))
* **codec:** a refused base64 value says what is wrong with it, not Go's offset into another dialect ([c03b61d](https://github.com/this-is-tobi/rta/commit/c03b61d6203929285f37880023159be21a46aadc))
* **codec:** a refused hex value names the group and the character, not Go's package ([64d9672](https://github.com/this-is-tobi/rta/commit/64d96723e4a0e7dabf466db75a73401febabebcd))
* **codec:** codec and debug text stops at what an agent can use, and --help says a pipe is read ([23e9aec](https://github.com/this-is-tobi/rta/commit/23e9aecd4dfe2cc1569c30e5a757347c042b0305))
* **config:** the file's header names no command and warns that comments do not survive ([f3e3dc9](https://github.com/this-is-tobi/rta/commit/f3e3dc9e1ed770156c0f15bc46a72120f217b616))
* **config:** the files an operator owns are opened without waiting, so a planted pipe holds nothing ([a15c179](https://github.com/this-is-tobi/rta/commit/a15c179a9555af07f1224d306263adcbef0132b0))
* **config:** the schema names the theme colours instead of sending an editor to a command ([19340a6](https://github.com/this-is-tobi/rta/commit/19340a645affec2c6a5d5d14dc26315cb142c67a))
* **debug:** ansi names a sequence by its prefix, intermediate and sub-parameters, not its final ([0bdd021](https://github.com/this-is-tobi/rta/commit/0bdd02157779d4a1d63c997753fe39fe0eaa916f))
* **debug:** ansi names the OSC commands that ask the terminal a question or tell a shell something ([df422d9](https://github.com/this-is-tobi/rta/commit/df422d92df4cf2376119230edbe7997cdc8d6b3e))
* **debug:** ansi reads an escape character written out as the character, and says it did ([450e99d](https://github.com/this-is-tobi/rta/commit/450e99d3ce48f20e0e10d4feb5d9f5c98ae113de))
* **eol:** check takes the version running for the cycle it belongs to ([3a8d911](https://github.com/this-is-tobi/rta/commit/3a8d91133eb042a4914f50eb05e5809ac024230e))
* **eol:** the day a release ends reads today, not "0d ago", and the last hour counts in minutes ([0f98a53](https://github.com/this-is-tobi/rta/commit/0f98a53995283c42ccfb0b5e5fee5d01569fdbf5))
* **explain:** the card says a grant names one of a list input's values, not "one targets" ([4a5213c](https://github.com/this-is-tobi/rta/commit/4a5213c481bfa947068a24f0dab0c8ca93732933))
* **findings:** an audit's rows reach a model with each detail whole, not clipped to a screen line ([220ecc7](https://github.com/this-is-tobi/rta/commit/220ecc78db97f7ae310379cc01ce3b4317588d19))
* **format:** a distance that rounds up to the next unit's whole is counted in that unit ([cb18513](https://github.com/this-is-tobi/rta/commit/cb18513b292464fcbaaa6990e19a895aa6500071))
* **format:** a size that one decimal rounds up to the next unit is counted in that unit ([c28db9e](https://github.com/this-is-tobi/rta/commit/c28db9e5662cab5df33f06ce6ca3cab9e9d34e3e))
* **fs:** a checksum that is not the file's is an error, and one of the wrong length is refused ([4996ea1](https://github.com/this-is-tobi/rta/commit/4996ea1f4546ab707360a27e55fd5ac5c0eda948))
* **fs:** a file name with a control character is shown escaped, not cleaned into another name ([7c1a0fd](https://github.com/this-is-tobi/rta/commit/7c1a0fd7b3b7af4d6fdcaafd1790c67a99e0cdf8))
* **fs:** usage counts the disk a file takes, and a file with several names once ([32ba724](https://github.com/this-is-tobi/rta/commit/32ba7242bf3b174b456ea17199ac8e6c7e308cc1))
* **gen:** a base64url token is unpadded ([0fcd77c](https://github.com/this-is-tobi/rta/commit/0fcd77c09a0ab1d41ac5ecaaf0c852b1ad6e84b8))
* **git:** a diff of a revision that matches nothing says where the existing ones are listed ([6a6e102](https://github.com/this-is-tobi/rta/commit/6a6e102e5cd27f3fd432b5f8f2e03296d3efae0b))
* **git:** a directory outside every repository is named whole, and one that is missing is said to be ([5400ae9](https://github.com/this-is-tobi/rta/commit/5400ae95edc83e8fee57e613997adbac5edb71c3))
* **git:** a path with a control character in its name is named, and the way out said ([0cf3ea1](https://github.com/this-is-tobi/rta/commit/0cf3ea1d0bc31a92bbb902404e575db7c08d75f6))
* **git:** a root the server cannot read hands its fix on as the operator's, not as a flag to type ([f415a88](https://github.com/this-is-tobi/rta/commit/f415a88e2566a51bff74d057ba11e95320d802d2))
* **git:** a submodule with a change inside it is listed as modified, as git lists it ([76c1e2e](https://github.com/this-is-tobi/rta/commit/76c1e2e3a7a50aaa9664f3e982d13a612442e78a))
* **git:** blame of a directory says it is one, not that the file was not found ([36516fc](https://github.com/this-is-tobi/rta/commit/36516fc16a0ed82dd0a2c34a6bba77294ffe50ae))
* **git:** diff of a merge commit says it is against the first parent ([5a35805](https://github.com/this-is-tobi/rta/commit/5a35805ff3828b9f1dcf6a87638a6d7e70b36cb6))
* **git:** diff of a revision that names no commit says what is wrong with the revision ([430e31c](https://github.com/this-is-tobi/rta/commit/430e31ca7618839278bb0a66666f93c3f534022b))
* **git:** diff of an empty file added or removed is its header alone, as git writes it ([3aaa7af](https://github.com/this-is-tobi/rta/commit/3aaa7af28f37c1c7fe752acf20ce6180904b5e87))
* **git:** diff shows a change of mode and a file added empty, which status already lists ([c19c004](https://github.com/this-is-tobi/rta/commit/c19c00472af2856b84b755fbb69c6e747b071656))
* **git:** git tools describe what an agent gets, not what git does, in 30% fewer bytes ([07c0c7e](https://github.com/this-is-tobi/rta/commit/07c0c7eadc45a37970a3e6552118d9adfd343f1a))
* **git:** git.hooks does not tell an MCP caller where the operator's own core.hooksPath points ([77b9d62](https://github.com/this-is-tobi/rta/commit/77b9d6264b3e2c2edcd9d49cf8de558b22ee667c))
* **git:** hooks lists what git would run before what it passes over ([1c6d79a](https://github.com/this-is-tobi/rta/commit/1c6d79aed5a1986c0b82aa722bbff407a44868be))
* **git:** log takes a directory for file, as git log -- &lt;path&gt; does ([f665393](https://github.com/this-is-tobi/rta/commit/f665393a908d1a2f73b36c036649e2480d5d886c))
* **git:** remotes says which URL git fetches from and which it pushes to ([ddf34b1](https://github.com/this-is-tobi/rta/commit/ddf34b11cdd056ae5ebbb5f70c87f389c42587dd))
* **git:** status lists the paths a merge left unresolved as unmerged, not as edits ([e591815](https://github.com/this-is-tobi/rta/commit/e591815cf8f44426222a32fe89a8d8b3334d27ae))
* **git:** status shows a path added with -N as new and unstaged, as git does ([a4a64b4](https://github.com/this-is-tobi/rta/commit/a4a64b4a581514dc0df53e7eadb4d7cff874df54))
* **git:** the overview says when it could not read the working tree ([71cfb40](https://github.com/this-is-tobi/rta/commit/71cfb40f6e80869f8cc94362e0a777bf920385b1))
* **git:** the repository's own root is a directory for file, not a path outside itself ([092f187](https://github.com/this-is-tobi/rta/commit/092f187a68913fcae82d5859d806f9831d86ed79))
* **grant:** a rate longer than a grant lives names the longest as 24h, not 24h0m0s ([0d3c361](https://github.com/this-is-tobi/rta/commit/0d3c361ccf7de6b68c0aee5c2b080f9268f3736f))
* **grant:** a refusal naming several records hands on a command for each, not the first ([be51d0a](https://github.com/this-is-tobi/rta/commit/be51d0a6ccf4f5e1d33e3d7cbdde771f6e7d29fa))
* **grant:** a revoke narrowed to an agent or a connection that matches nothing says so ([fbf72bd](https://github.com/this-is-tobi/rta/commit/fbf72bdade6c0d42fcdcc1b0844a039ce2afc06b))
* **grant:** a ttl that is no duration is refused without Go's sentence, and 1d says what to write ([0a2acac](https://github.com/this-is-tobi/rta/commit/0a2acac17f5210f43ffc0614035aca5705c3ed9c))
* **grant:** the refusal to guess whom a grant is for names the agents the log has seen ([15e9549](https://github.com/this-is-tobi/rta/commit/15e9549644e95dde23278ce13a0ae57cb2e2778f))
* **guard:** a roster too large for the guard to read back is refused before it is written ([6edc3a6](https://github.com/this-is-tobi/rta/commit/6edc3a6148d504c2e9c7925e1af4d8fbf961d757))
* **http:** a certificate that did not verify points at the one the server presented ([ecddc02](https://github.com/this-is-tobi/rta/commit/ecddc02ef62a3ddfe66492d02280c1e92bc8b3e9))
* **http:** a header whose value has commas is one header on the command line and in the form ([ac03fec](https://github.com/this-is-tobi/rta/commit/ac03fec39ec763da4c7a11cbf17fbe721cc270a8))
* **http:** a URL whose scheme the client does not speak is not blamed on the network ([efbd81a](https://github.com/this-is-tobi/rta/commit/efbd81a2732ffcb6d9fe33e584ac09d93b183e51))
* **http:** an address typed as the host is refused as itself, not as a name that resolved ([6c9454f](https://github.com/this-is-tobi/rta/commit/6c9454f653e9406257f2aca184a95ee9b55277e0))
* **http:** http_status says it looks at no URL, and which tool reports a live one's status ([ce0aa91](https://github.com/this-is-tobi/rta/commit/ce0aa913200600cc7e20cd2933214dcd823e48b7))
* **http:** the hint follows how far the request got, not one line for every failure ([d5ffa00](https://github.com/this-is-tobi/rta/commit/d5ffa0078486e6f0d729bbc0b549e14c34f07fd5))
* **init:** the dashboard tile choices are cut to the form's width instead of wrapped ([9ed9179](https://github.com/this-is-tobi/rta/commit/9ed917999488f81305f08aaa96a7262e97f5bcd2))
* **keys:** a key is refused a missing directory before the words are asked for ([b53ca2c](https://github.com/this-is-tobi/rta/commit/b53ca2c4dbdf5ede8f99827c209d5e528c173c12))
* **keys:** a public key given to backup is told to be the public half, not "no key found" ([d743ef9](https://github.com/this-is-tobi/rta/commit/d743ef9705df48824571d535e2fbebb76392f786))
* **keys:** keys.backup no longer cites capabilities that were never written ([937fa41](https://github.com/this-is-tobi/rta/commit/937fa415a851c2f674034ba6f4799e5155edd222))
* **kv:** kv and keys text names only inputs an agent can give, and fits the budget ([f15da44](https://github.com/this-is-tobi/rta/commit/f15da4457a6a44d89ca22b5c55dce45a5dfec08d))
* **kv:** the hint for a missing entry hands on the listing as the operator's to run ([321aa64](https://github.com/this-is-tobi/rta/commit/321aa64f6d43940ef40b53ef8a2ac2f05eafd084))
* **lockdown:** a credential lock refused for its name points at the agent log, not the ledger ([536b718](https://github.com/this-is-tobi/rta/commit/536b7187bdf1ab653a4e0c77c79e7ceb93b521a9))
* **lockdown:** a server with no verified locks refuses every call while the lock file is unverified ([990411e](https://github.com/this-is-tobi/rta/commit/990411e19c4f73c4cd10366072c8dccc0c182b74))
* **lockdown:** the lock held for a principal is a field of its own, not a By nobody may spell ([0796e14](https://github.com/this-is-tobi/rta/commit/0796e146d3cc93843e12d861d4f00ae7ade64069))
* **mcp:** a call that needs a grant is refused while the record is unwritable, not run unrecorded ([b475412](https://github.com/this-is-tobi/rta/commit/b475412786ed19147d99fe4b4972414445e1c639))
* **mcp:** a config that will not read keeps the profiles the server started with, not none ([d22977c](https://github.com/this-is-tobi/rta/commit/d22977cb24f747ffb692e741c6badfa323ef7558))
* **mcp:** a credential in a URL or a header is masked in the record and in the errors an agent reads ([b3a778e](https://github.com/this-is-tobi/rta/commit/b3a778ee93a91bf8e76c2708c3b55b31adb3fb0b))
* **mcp:** a description naming another capability says what its tool is called ([eeb06be](https://github.com/this-is-tobi/rta/commit/eeb06be991d23b9453870d637ead49af06cace8a))
* **mcp:** a loop of calls that need no grant is held to a rate the record can keep up with ([41501cc](https://github.com/this-is-tobi/rta/commit/41501ccaab18bbc94ddf8ef394abe5a81482a658))
* **mcp:** a refusal for a value of the wrong type says what the input takes, not only its type ([52ca6af](https://github.com/this-is-tobi/rta/commit/52ca6af2dc9643b774cff9d9550f7e3e8f52a0ae))
* **mcp:** a result does not tell an agent where rta keeps its stores any more than an error does ([ba66852](https://github.com/this-is-tobi/rta/commit/ba66852d0ba5cf99c95c6f30ee8ddffca9fe7659))
* **mcp:** a result is sent once, not again as structured content ([bd8921e](https://github.com/this-is-tobi/rta/commit/bd8921eedede15a763cfb1ffbe0425d33c4fdc22))
* **mcp:** a server whose config never read refuses what a profile could govern, not runs it ([f60a38c](https://github.com/this-is-tobi/rta/commit/f60a38c498b6aa9bc397686651852f7e6afe8f56))
* **mcp:** a stdio request over the size the listener allows ends the session, not read whole ([11f50ff](https://github.com/this-is-tobi/rta/commit/11f50ffa05a370ea19124a0750955afbf613cfe1))
* **mcp:** an argument written snake_case is told the kebab-case name it is a spelling of ([0b82484](https://github.com/this-is-tobi/rta/commit/0b82484967a427256c3b573f0d76c07559322b00))
* **mcp:** an error does not tell an agent where rta keeps its stores, grants and record ([2381f88](https://github.com/this-is-tobi/rta/commit/2381f88f895e79a9f3c7fb89853920c12ef4324b))
* **mcp:** an input only the operator sets is refused to a caller like any name the tool lacks ([95262f9](https://github.com/this-is-tobi/rta/commit/95262f92147c7df83e6672d736848220f40d48dc))
* **mcp:** an unknown client is answered with the clients that exist ([daa693c](https://github.com/this-is-tobi/rta/commit/daa693ca0dc69549569bc89f9d69e1b536f9a80d))
* **mcp:** descriptions stop restating their summary and telling a story a model cannot act on ([94cc660](https://github.com/this-is-tobi/rta/commit/94cc660dbe740765cd27bbb30b24cdb4dfd04076))
* **mcp:** descriptions stop sending a model to surfaces and capabilities it does not have ([fd352e7](https://github.com/this-is-tobi/rta/commit/fd352e7f4d384f85cfdc9bf308498ceb23790616))
* **mcp:** four descriptions that grew past the budget are cut back to it ([41b5019](https://github.com/this-is-tobi/rta/commit/41b501981e766b5788e16bc0ec7deac3e4b93e9e))
* **mcp:** installing into a client that already holds rta says so, not what to add again ([0dcbbcc](https://github.com/this-is-tobi/rta/commit/0dcbbccb56dee38bac2542f5b53cd73fcce1a423))
* **mcp:** readiness asks whether the record takes an append, the question a gated call is asked ([7fe8976](https://github.com/this-is-tobi/rta/commit/7fe8976881ed31e36da85dc28ac817902e400526))
* **mcp:** stopping a stdio server cancels the calls in flight and waits ten seconds for the rest ([40c7fcd](https://github.com/this-is-tobi/rta/commit/40c7fcdf199e03f5dbc4295356c171fd63184445))
* **mcp:** the handshake says a table's total counts rows the list did not send ([75627aa](https://github.com/this-is-tobi/rta/commit/75627aa56aa680436d74f3142aea39d8b0d0b147))
* **mcp:** the open readiness probe names the data directory by what it is, not by its path ([baef1d2](https://github.com/this-is-tobi/rta/commit/baef1d20832f8ab09874f57d14eea0c3c428e7e5))
* **mcp:** the server is titled RTA, as the docs title the project ([ed69822](https://github.com/this-is-tobi/rta/commit/ed698221877756d0d5ba5b50a05546f859bc0c2a))
* **net:** a host that does not resolve is not blamed on its port ([ed427c9](https://github.com/this-is-tobi/rta/commit/ed427c9ed0b5e22642d5cd8714d5d58d4372bdc0))
* **net:** a port range typed backwards is told so, not called out of bounds ([0999baa](https://github.com/this-is-tobi/rta/commit/0999baac05d6f4689b894a6f85aaf90a697f330e))
* **net:** a scan of a host that does not resolve is told so, not drawn as a column of closed ([27fc635](https://github.com/this-is-tobi/rta/commit/27fc63531b365551939d5739ab2d40573c32ede6))
* **net:** hosts add takes names with the shape of a hostname, not any text without white space ([fa8286f](https://github.com/this-is-tobi/rta/commit/fa8286fa2ace83be21e27c72797b22242143b8a7))
* **net:** net_probe says it sends nothing, not that it listens ([98bf1ff](https://github.com/this-is-tobi/rta/commit/98bf1ff918f8ceb2ad0ed6a38d8b2e1c5f3192a0))
* **net:** trace names the next step when a host does not resolve, as ping does ([ff96520](https://github.com/this-is-tobi/rta/commit/ff96520b6df3e7936aec233baded9439b42591ce))
* **note:** a tag given twice is one tag, and a note is counted once for it ([aaf8460](https://github.com/this-is-tobi/rta/commit/aaf8460785257e46767f683e5ee7862f8d96d2c5))
* **operator:** the refusals and advice that name the record call it the record, not an audit trail ([e2ddcf3](https://github.com/this-is-tobi/rta/commit/e2ddcf38f3d387ccbc447519f867d41beec198ae))
* **paths:** a stranded directory another account made is refused to a reader, not only a writer ([eca0b1c](https://github.com/this-is-tobi/rta/commit/eca0b1c0d63c5932e03a47c4c13f39243da00980))
* **plugin:** "up to date" names the index update that could change it ([2cc13ee](https://github.com/this-is-tobi/rta/commit/2cc13ee6ab65ecb621dade55320af7215764aff7))
* **plugin:** `plugin new` refuses a name a plugin on this machine already answers to ([742669e](https://github.com/this-is-tobi/rta/commit/742669e86a07456ac6623650f35d552f5c75d3fe))
* **plugin:** a name where the plugin binary is wanted says what is wanted ([f3a08b0](https://github.com/this-is-tobi/rta/commit/f3a08b0caf872135c75c04128c1c2b1947e1b1e7))
* **plugin:** an install spec that is no name is quoted as typed and says what to do ([8d55414](https://github.com/this-is-tobi/rta/commit/8d55414f4fdf1ad56abb972fbe802091e48ff8be))
* **plugin:** an upgrade names the profiles it left stale and the command that repins them ([8c44a4b](https://github.com/this-is-tobi/rta/commit/8c44a4bffde3691f48d2429af40b3b25421b936d))
* **pluginhost:** a launched plugin keeps TEMP and TMP, TMPDIR's Windows spelling ([a7dd58b](https://github.com/this-is-tobi/rta/commit/a7dd58b4e4085fee3bee46eaa423b7cbe9022405))
* **pluginhost:** a line of a plugin's last words that was cut says so ([b5245bf](https://github.com/this-is-tobi/rta/commit/b5245bf497abfd55dfb6577345f841ce8223d51d))
* **pluginhost:** a named pipe with the execute bit is refused, not waited on for a writer ([a6b746e](https://github.com/this-is-tobi/rta/commit/a6b746e094e783151016d9693f24110657a2b33c))
* **pluginhost:** a plugin on $PATH runs from the copy whose digest was checked, not from its name ([58c4fbf](https://github.com/this-is-tobi/rta/commit/58c4fbf7fcc3729bda82a7f70171b878e75c75c1))
* **plugin:** install, upgrade, remove and the index commands complete the names they take ([f14235b](https://github.com/this-is-tobi/rta/commit/f14235b4476421bf527a3cf3e8a6c7e4306e524a))
* **plugin:** removing a name nothing manages says so before asking for consent ([562bd36](https://github.com/this-is-tobi/rta/commit/562bd3614df46d7bcd4d3e179884e38b178ff215))
* **plugin:** the install report says where a plugin's keys go in a sentence ([8557fb9](https://github.com/this-is-tobi/rta/commit/8557fb9704cf1f30c42683e6c76d43bfd3376ce9))
* **plugin:** the install report sends a plugin with no config keys to its call, not to the config ([da9933e](https://github.com/this-is-tobi/rta/commit/da9933ee15f470a86499660a85bfd4c7c4197baa))
* **policy:** policy require reads the policy file without waiting on a pipe put in its place ([365348a](https://github.com/this-is-tobi/rta/commit/365348af1d516d34d5f365ca95ce8bcb17afea9e))
* **profile:** a labeled instance is asked for a secrets mapping, not for a variable it never reads ([dea3b9a](https://github.com/this-is-tobi/rta/commit/dea3b9ad39ce3ee99e45b767051df0f72f8fa430))
* **profile:** a selection file that does not verify holds every profile shut, not off ([2d1d222](https://github.com/this-is-tobi/rta/commit/2d1d2228690a66ecfe40e1c4e36a74834f3d41c9))
* **profile:** a stale or short pin names the command that repins every profile ([b7be3ff](https://github.com/this-is-tobi/rta/commit/b7be3ff1a030080f76b453481d11a39de608ad7d))
* **recent:** a header that carries a credential is not remembered for completion ([0937842](https://github.com/this-is-tobi/rta/commit/0937842fa9dfcfdb185cc42b083663a55d62b43e))
* **recent:** a header with commas in its value is remembered whole, not as its halves ([5aae462](https://github.com/this-is-tobi/rta/commit/5aae462f1e32bf2a71797a0b0f72ad294f53f75c))
* **recent:** a URL with a credential in it is not remembered for completion ([e6137e3](https://github.com/this-is-tobi/rta/commit/e6137e39dd16b72788d80f8ee34dc458a71e0cdd))
* **render:** a table too wide for its terminal is arranged by rta, not left to lipgloss ([53649f4](https://github.com/this-is-tobi/rta/commit/53649f4b00f4eabb4828f931b331443c15d2d538))
* **render:** a value with no room beside its key goes under it instead of past the edge ([3da02b8](https://github.com/this-is-tobi/rta/commit/3da02b8920001d594c752c7b03089956409197d2))
* **render:** csv leaves a lone dash, a number and a refspec as they are, guards what can run ([f4fc386](https://github.com/this-is-tobi/rta/commit/f4fc38676228043e6354dcb1f6b6d669993f2486))
* **theme:** ok, warn and error text reads on a light terminal as well as a dark one ([edac300](https://github.com/this-is-tobi/rta/commit/edac3003db369406da88c7ddb7939894f105dabe))
* **time:** a clock reading shown twice names the other instant beside the answer ([001cffa](https://github.com/this-is-tobi/rta/commit/001cffa125bb4c3991af0fd278852bbfe459d385))
* **time:** a time the clock skipped is refused, not moved an hour and answered as the one typed ([2fa3ef7](https://github.com/this-is-tobi/rta/commit/2fa3ef767f7f60d336569e1feff29ea6309f0809))
* **time:** the short ISO spelling 2026-09-04T12:30 is read like the space form beside it ([23d71f3](https://github.com/this-is-tobi/rta/commit/23d71f3750b574572d04ba60954f8fd7f801e4db))
* **tui:** a dashboard with no tiles says how to get them back ([035b695](https://github.com/this-is-tobi/rta/commit/035b695b7c80e9a2c7cb3eedc9232614720616d1))
* **tui:** a footer notice that reports a failure wears a cross, not the success check mark ([929ba82](https://github.com/this-is-tobi/rta/commit/929ba8211ebe77e51a8c5dcc556685927b9361cb))
* **tui:** a footer that drops keys keeps "? help", the key that lists them ([5386336](https://github.com/this-is-tobi/rta/commit/538633688c4ac227c7413edc0b09953314b50673))
* **tui:** a narrow footer keeps how to leave, the key that lists the rest and a flash ([25c44c3](https://github.com/this-is-tobi/rta/commit/25c44c3709b8ea7fba8f753726749cc17f162471))
* **tui:** a narrow result keeps its "partial" warning and drops the counts beside it ([4f6a6d9](https://github.com/this-is-tobi/rta/commit/4f6a6d9d92470b8a6a7d7b4eeed3cfb3f6f8c334))
* **tui:** an empty profiles pane offers only to make one and to leave ([57fd584](https://github.com/this-is-tobi/rta/commit/57fd5847ac8c36c6db14e9450beef37fac7f59ce))
* **tui:** an environment holding no plugin offers only to add one and to leave ([7014103](https://github.com/this-is-tobi/rta/commit/70141037cdd0873016ebaae46ce2ba2f466ad69f))
* **tui:** clicking a tile while the search box is open spends the query ([b509fe1](https://github.com/this-is-tobi/rta/commit/b509fe10149195d573532fcf8426ce9a559de785))
* **tui:** copying a view as JSON reaches the system clipboard, and says so only when it did ([b692e8c](https://github.com/this-is-tobi/rta/commit/b692e8c8b3d992116fc295f51fcfd8cd31a275af))
* **tui:** filtering the catalogue puts the cursor on the first match ([90edd47](https://github.com/this-is-tobi/rta/commit/90edd47054997142bba5fba2039ed6264cfbdd56))
* **tui:** saving a new profile puts the cursor on it ([6a331e9](https://github.com/this-is-tobi/rta/commit/6a331e91a1d7367aed022ceaf4fd64f9bf8292cb))
* **tui:** the arrows on the profiles panes are labelled select, as on every other list ([ac40f68](https://github.com/this-is-tobi/rta/commit/ac40f681fc54042dd69e1996da2288689485a955))
* **tui:** the bar over the catalogue's filter box names the keys the box answers ([2791a42](https://github.com/this-is-tobi/rta/commit/2791a420fb8c700c658a6a48067afecce2e92f58))
* **tui:** the catalogue is sized to its footer after every message, so a hint is not cut off ([f750262](https://github.com/this-is-tobi/rta/commit/f7502622f0852e53ea6e985aa9e8e2ae3d847dcb))
* **tui:** the catalogue's filter ranks like the dashboard's search, not on scattered letters ([26cc767](https://github.com/this-is-tobi/rta/commit/26cc767babb855555b384246ba2bbe614e41418c))
* **tui:** the catalogue's list follows the footer it is drawn with, so a flash cannot cut the bar ([f5a7c26](https://github.com/this-is-tobi/rta/commit/f5a7c26c41b968a6cd40de9c243758da8e819ab0))
* **tui:** the dashboard's footer stays on a terminal ten or eleven rows high ([ab5b6d6](https://github.com/this-is-tobi/rta/commit/ab5b6d6c5008c71f667138fa35f9cded995c5304))
* **tui:** the help overlay says how to close it in its border, where a short terminal cannot cut it ([1f5d5b8](https://github.com/this-is-tobi/rta/commit/1f5d5b856a008ac0580e882245d8553166bff59e))
* **tui:** the key overlay does not list the space bar's second name as a blank ([83c248d](https://github.com/this-is-tobi/rta/commit/83c248d8c165d66f531c47a7534f83659b98b654))
* **tui:** the profile editor refuses a bad name or colour at the box, and keeps the form ([e832036](https://github.com/this-is-tobi/rta/commit/e832036cc5b30fde22f2a814ea84b381282b49c7))
* **tui:** the search box's footer offers the keys the box answers, not the dashboard's ([8ae4f24](https://github.com/this-is-tobi/rta/commit/8ae4f244487775430157028dcf7cf87f6afbf354))
* **tui:** the selected tile is drawn in heavy lines on a terminal that shows no colour ([ede56eb](https://github.com/this-is-tobi/rta/commit/ede56ebffe07122d199a97829e1b16ce89f95889))
* **tui:** the theme editor drops its swatch strip before it cuts off the box being typed into ([2c45e8e](https://github.com/this-is-tobi/rta/commit/2c45e8eb18b948711cdac7f4c079568cc6e8137d))
* **tui:** the theme editor's swatch strip wraps by swatch and name, never between them ([0659268](https://github.com/this-is-tobi/rta/commit/065926805b8c516d9fd4eb48d985883fa13c0273))
* **tunnel:** a cluster that did not answer is told as that, not as kubectl's klog line ([d85bf33](https://github.com/this-is-tobi/rta/commit/d85bf33d56cb5c4cc0b607a2c39d46d30fd9d940))
* **tunnel:** a port-forward does not outlive a server that was killed, where the platform can say ([e68d877](https://github.com/this-is-tobi/rta/commit/e68d8777ab18b0ec4b36934b7087a6b16bbaa3d4))
* **tunnel:** a refused kube context is named, not only the profile that used it ([b4c99b8](https://github.com/this-is-tobi/rta/commit/b4c99b880e461b0e0b162bb280e745dec6206477))


### Performance Improvements

* **agentlog:** the last hour is read from the end of the record, not by parsing the whole segment ([5c34712](https://github.com/this-is-tobi/rta/commit/5c347122ed6cfe38982a94cb7098652f795c1f30))
* **agent:** the metrics count the record as they read it instead of holding all of it ([6428cb6](https://github.com/this-is-tobi/rta/commit/6428cb655e98aa6fb8c51c06a438fdc695abcea1))
* **app:** the command tree is built from the config main already read, not from a second parse ([c7d2770](https://github.com/this-is-tobi/rta/commit/c7d2770b502f3945d83f00aa253b59dd674cee05))
* **operator:** the dummy key for an unknown fingerprint is a constant, not made at every start ([ff92004](https://github.com/this-is-tobi/rta/commit/ff9200492fb7b1716e530ff22fe73899bc772002))
* **plugindist:** the two {0,127} patterns compile when first checked, not at every start ([49f2748](https://github.com/this-is-tobi/rta/commit/49f2748494d60d18658fdd8bd1a869c0bbe9b416))
* **pluginhost:** the deny set is resolved when a plugin is first launched, not before the sweep ([fa8e194](https://github.com/this-is-tobi/rta/commit/fa8e1945877858b7f2b11a3c184565f5f956d280))

## [0.33.0](https://github.com/this-is-tobi/rta/compare/v0.32.0...v0.33.0) (2026-10-02)


### ⚠ BREAKING CHANGES

* **profile:** beside a profile's kube: or ssh: forward, a CA input the forward leaves unread, such as --sslrootcert or mysql's --ca-file, is refused rather than accepted and ignored.
* **profile:** beside a profile's kube: or ssh: forward, a TLS input asking for TLS, such as --sslmode require or --tls, is refused rather than connecting directly to the configured or default host; give the host and port as well to connect directly.
* **tui:** a TUI list box reads a double quote that opens an element as CSV does. Text with such a quote left open, or with text after its closing quote, is refused, and "x" is the element x rather than the text with its quotes.

### Features

* **plugin:** a certificate macOS refuses for its validity period has a hint naming Apple's limit ([f3f0be1](https://github.com/this-is-tobi/rta/commit/f3f0be1700721d0aa19dbf67de8fdf97a4ee0629))
* **plugin:** the SDK names where a call reached and reads a certificate's names and plain HTTP ([073c450](https://github.com/this-is-tobi/rta/commit/073c45051aef2a0921b886bc3c096b1f4f34f429))
* **plugin:** the system's verdict that a certificate is revoked is read by CertRevoked ([2260e4b](https://github.com/this-is-tobi/rta/commit/2260e4b04f49a8a1b6619d144ee5055b1e02c935))


### Bug Fixes

* **audit:** a lockfile the bounds withhold is named as withheld, not taken for one not there ([9dab5f3](https://github.com/this-is-tobi/rta/commit/9dab5f34b832b7f74b23b682780beed4dc87035d))
* **cert:** a chain macOS refuses for its validity period says the rule and its fix ([154d913](https://github.com/this-is-tobi/rta/commit/154d913f73e7af8065e5d77bb9b8165e4b7cec9d))
* **cert:** a target the bounds refuse is refused as they refuse it, and not dialled as a host ([f66f965](https://github.com/this-is-tobi/rta/commit/f66f96514dca783fa1706b77b0ec4aa8fcd8fdc0))
* **clipboard:** a program that fails is reported by its own error, not as timed out ([ab7a37a](https://github.com/this-is-tobi/rta/commit/ab7a37a7b2e4d18339df796f2ec76389288f8449))
* **doctor:** a profile problem said once names the entries it is about, as the profile's page does ([6d5b62c](https://github.com/this-is-tobi/rta/commit/6d5b62ca7e33069c165752198a5851d52d8469c6))
* **git:** a file git opens under a root is refused as rta's own state by its identity ([0df4974](https://github.com/this-is-tobi/rta/commit/0df49743603b48b04ea58f0ff962d95df1c5d52f))
* **git:** a file the repository's filesystem opened is judged by its own Stat, not its name ([ffe4542](https://github.com/this-is-tobi/rta/commit/ffe4542328378fe0ff29847db072aec50679850f))
* **git:** a name git walks on Windows is taken apart at either slash, as Windows opens it ([b020fd2](https://github.com/this-is-tobi/rta/commit/b020fd262043165c135db9153877878dc95b22d4))
* **git:** a root that cannot be listed is named, and a repository under a root inside it is served ([ad25b8a](https://github.com/this-is-tobi/rta/commit/ad25b8aea01a134bc07e77a4cd0b7816c76457fc))
* **grant:** an expired grant is not written back, and a store too big to read is refused ([1169443](https://github.com/this-is-tobi/rta/commit/116944332a87dc871d2c59beed28db75bf133845))
* **http:** a request macOS refuses for its certificate's validity period says the rule ([71a020a](https://github.com/this-is-tobi/rta/commit/71a020a3a93499b6f9db7500c238e3cede8e4a48))
* **mcp:** a server says once at start which root it cannot read, and how to make it readable ([76d3b7e](https://github.com/this-is-tobi/rta/commit/76d3b7e3528ae830599aeef66890cad75b3e743b))
* **net:** a hosts file or resolv.conf the bounds refuse is refused as they refuse it ([627bb2e](https://github.com/this-is-tobi/rta/commit/627bb2e7f0de3c8c772c56676ecf1e26d9110dd5))
* **pathguard:** a link's target on Windows is taken apart at either slash, as git's walk takes it ([2a5ad4a](https://github.com/this-is-tobi/rta/commit/2a5ad4a7e457ba4efff82841cede2b468e9d678a))
* **pathguard:** a path is opened from the deepest root holding it, an outer one only if that fails ([af72247](https://github.com/this-is-tobi/rta/commit/af72247fc214e214325f84586b28cdd8313f118e))
* **pathguard:** rta's own state is refused by its files' identity as well as by their names ([4ef4e16](https://github.com/this-is-tobi/rta/commit/4ef4e16c6eda1839f8c7d6cea4f41a3dab9e99af))
* **paths:** a machine with no home keeps its state in a private directory, not the current one ([3ee1914](https://github.com/this-is-tobi/rta/commit/3ee1914d4c9144a83972aae427fca08c484bead2))
* **plugin:** a dial's questions answer no error a server that was reached gave, whatever its words ([6e57edd](https://github.com/this-is-tobi/rta/commit/6e57edd35d39f92d380b089c177a87c8c23df090))
* **pluginhost:** a call that raced its plugin's death reports the death, not a transport failure ([d559a8b](https://github.com/this-is-tobi/rta/commit/d559a8b9015ad9f5f3b4be35948f2cf542450409))
* **pluginhost:** a failed launch stops waiting on a descendant that holds the plugin's output ([78b6d5d](https://github.com/this-is-tobi/rta/commit/78b6d5dd3eb6df82d88f3c342c1d5fc0979e9781))
* **pluginhost:** a plugin that exits before its handshake is said to have, with its last lines ([7b9df54](https://github.com/this-is-tobi/rta/commit/7b9df541c7e326ba6b7a1670dc8500a582f12075))
* **pluginhost:** a plugin that prints in place of its handshake is said to have, in rta's words ([addec73](https://github.com/this-is-tobi/rta/commit/addec73ce48b4ea49a9001c2c95c8788a0576357))
* **pluginhost:** a start that failed is reaped and collected before rta says how the plugin ended ([d2c0928](https://github.com/this-is-tobi/rta/commit/d2c09285e85c9aae0504f5bd22af8f0613f81ab2))
* **plugin:** on Windows ExpandHome reads ~\ as the home directory, as it reads ~/ ([43087c8](https://github.com/this-is-tobi/rta/commit/43087c80fbdb69e943f5c8145a20a918fe17ac43))
* **profile:** a CA given on a call that goes through a forward is refused, not left unread ([1a54a89](https://github.com/this-is-tobi/rta/commit/1a54a89f336e946936dbc90620be45c3970e0782))
* **profile:** a TLS switch beside a forward names no server, and one asking for TLS is refused ([307a9ea](https://github.com/this-is-tobi/rta/commit/307a9ea5b67f2b9e81621ba0f37b5b3528aa236a))
* **tui:** a fetch lands in a completing field through the form, never by clearing its function ([a53166a](https://github.com/this-is-tobi/rta/commit/a53166a9fe593d14c102a161dc6ae6e7c95ca9ca))
* **tui:** a form settles every command to its answer and leaves alone only a clock ([163a9a1](https://github.com/this-is-tobi/rta/commit/163a9a1a9d618be93d50dd5943210a4f0e491519))
* **tui:** a list box reads the list flag's grammar, so an element may hold a comma or edge spaces ([b648e19](https://github.com/this-is-tobi/rta/commit/b648e19fb485550081b0f3d14e9e454be804c4b6))
* **tunnel:** a deadline spent before kubectl or ssh starts is the timeout, not a failure to start ([6693482](https://github.com/this-is-tobi/rta/commit/66934821fb4f43485ff2492492ed7645547e8301))
* **tunnel:** a forward or ssh child that ignores SIGTERM is killed when Close gives up waiting ([0b28ac5](https://github.com/this-is-tobi/rta/commit/0b28ac5f640e85600ebc57acc73fb1ef62f9acfb))
* **tunnel:** a secret read that outlasts its deadline is the timeout, not an unreadable secret ([7884663](https://github.com/this-is-tobi/rta/commit/7884663006f1f9eeeaa1425a23051973256c1ddf))
* **tunnel:** an exit rta's own kill caused is the timeout, before the wait's context hears of it ([716838f](https://github.com/this-is-tobi/rta/commit/716838f351b509177f4a0cc99e35b2273e7a17fd))

## [0.32.0](https://github.com/this-is-tobi/rta/compare/v0.31.0...v0.32.0) (2026-09-29)


### ⚠ BREAKING CHANGES

* **git:** a repository whose extensions.relativeWorktrees is a number git does not read, such as 08 or 3g, is refused as git refuses it.
* **git:** git.remotes, git.branches and git.overview refuse a repository whose config sets a remote's url, pushurl, fetch or other string key, a branch's remote, pushRemote or merge, a url.<base>.insteadOf or remote.pushDefault with no value at all, a fetch or push refspec git cannot parse, or a remote's boolean to a value git does not take, as git refuses to run with it.
* **git:** over MCP a file of git config inside the server's roots, the operator's own among them, is held to what the repository's config is held to, so an include there of a file outside the roots is not followed, a hasconfig condition there matches the repository's own remotes alone, and a core.excludesFile there naming a file outside the roots is not applied. A config file reached through a link inside the roots that leads out of them is not read over MCP.
* **fs:** under an MCP root, fs.tree and fs.usage name rta's data directory and a configuration file of its as withheld, where they listed what the directory holds and sized both.
* **mcp:** over MCP, audit.deps no longer reads a lockfile its scan reaches through a link that leads out of the roots, and a path under a directory the server may search but not list is refused as unreadable, since each directory on the way down from the root is opened.
* **git:** git.remotes rows carry a fourth column, Origin, and git.remotes is refused where a file of config git reads cannot be read, as git refuses to run with one.
* **pluginhost:** a plugin's socket sits one directory deeper in TMPDIR, so a TMPDIR longer than 66 bytes on macOS or 70 on Linux no longer leaves room for its path; up to 86 and 90 did before.
* **git:** git.hooks refuses a core.hooksPath under git's install prefix or under the home of a user it cannot look up, where it listed the directory of that name in the working tree.
* **git:** a config whose includes git refuses to run with (a cycle, an include past ten deep, a file that cannot be read or is not config, a relative include in the environment, or a remote URL in a file an includeIf includes beside a hasconfig condition) is now refused by every capability that reads the config, as git refuses it, and so over MCP is one including a file the path gate refuses other than as outside the roots. git.config rows carry a fourth column, Origin.

### Features

* **plugin:** a failed connection is read by the error the system gave, not the OpError around it ([5118446](https://github.com/this-is-tobi/rta/commit/5118446090d0cdde0d64fbbf4fc4f951d7eb1f3f))
* **plugin:** a hint for an untrusted certificate names the CA file and says it replaces the checks ([ee16212](https://github.com/this-is-tobi/rta/commit/ee16212b7065a36332f9749c59f103de03c62a55))
* **plugin:** a request says which profile it came through and the kind of forward opened on it ([6655156](https://github.com/this-is-tobi/rta/commit/6655156e7ce514711e7ae4b3e5c2b8d2a81504cc))
* **plugin:** an input a caller gives is named with its value as Call spells that argument ([798905b](https://github.com/this-is-tobi/rta/commit/798905bab7d41c701e406f7b319bc1758d7fef67))
* **plugin:** the SDK names a connection setting per surface, to an agent as the operator's ([f7e7bf6](https://github.com/this-is-tobi/rta/commit/f7e7bf602398dd1845b3efe4134fb78a422727db))
* **profile:** profile show takes an instance reference and shows that one connection ([3456c90](https://github.com/this-is-tobi/rta/commit/3456c904a0835be3329d7ee321f8e69c81764a6e))
* **sdktest:** an input the source gives through InputTo is one an agent gives as an argument ([1f17ac5](https://github.com/this-is-tobi/rta/commit/1f17ac515e86127902e88dfe9ce49c387cfefce2))
* **sdktest:** the suite reads a plugin's own source with WithSource, as each plugin's copy did ([31bf406](https://github.com/this-is-tobi/rta/commit/31bf406672ff991d6c6f63d7b0074de59648cd8b))


### Bug Fixes

* **doctor:** each sentence doctor and the plugin notice build on a count agrees with it ([3d94aaf](https://github.com/this-is-tobi/rta/commit/3d94aafa7015ea50439784cdaeb02b89ba669cc3))
* **doctor:** the sandbox row counts its paths and directories in the number they come in ([a0689a1](https://github.com/this-is-tobi/rta/commit/a0689a125ce30bbf9dbca4af3e1b1554258a4f91))
* **fs:** tree and usage say what a path that is not a directory is, not that it is a file ([576fe42](https://github.com/this-is-tobi/rta/commit/576fe42dda8d759c9303859cc2519261bdffc3b3))
* **fs:** tree and usage walk from the directory they opened, and stop at rta's own state ([c69196e](https://github.com/this-is-tobi/rta/commit/c69196e4243218de02f5be66939e47d2c9fffa75))
* **git:** a boolean in git's config is read as git reads its number, in hex and octal too ([de425a9](https://github.com/this-is-tobi/rta/commit/de425a9e69d84611ae9fff1ff8bad5ccf538cdc4))
* **git:** a branch that tracks another branch of the repository is counted against it ([12cd612](https://github.com/this-is-tobi/rta/commit/12cd6125142c2c7a03cb45b6e855681cbf4b810c))
* **git:** a branch whose section names a remote and no merge tracks nothing, as git reads it ([4b59b52](https://github.com/this-is-tobi/rta/commit/4b59b52d84f03eed2430737ab24356ea6b7f5599))
* **git:** a branch's upstream at a remote is found through its fetch refspec, as git finds it ([637ef77](https://github.com/this-is-tobi/rta/commit/637ef7781b8f687134dec7745a7dc68d0a4b7fc3))
* **git:** a config key naming a credential anywhere in its name has its value masked ([e0fea47](https://github.com/this-is-tobi/rta/commit/e0fea479723ef453639fccb6fd25636b6778073d))
* **git:** a core.hooksPath or core.excludesFile under ~user is that user's home, as git reads it ([905fd40](https://github.com/this-is-tobi/rta/commit/905fd40358667ccc54777244f73cf249206f94c0))
* **git:** a file git's config includes is read as git reads it, and shown where it may be ([d312472](https://github.com/this-is-tobi/rta/commit/d312472dab817951b1c1eca2084c7988daa5d90b))
* **git:** a remote and what a branch tracks are read from every file of config git reads ([2c3d180](https://github.com/this-is-tobi/rta/commit/2c3d1807be8f36ddcb9caab7df6cb06a1da61c04))
* **git:** a remote or branch key git refuses to run with, set with no value, is refused ([7f3a48f](https://github.com/this-is-tobi/rta/commit/7f3a48f700bf4713c68d797937478a3ee10cfa6f))
* **git:** a repository opens where go-git refuses its branch or remote config and git reads it ([4c1a4a5](https://github.com/this-is-tobi/rta/commit/4c1a4a566f99519b3846497dfa326e31adbcdd52))
* **git:** an excludes file linked out of its directory is read through the link, as git reads it ([6f04cf8](https://github.com/this-is-tobi/rta/commit/6f04cf802629c02a93d5efd660456ce9e9e10333))
* **git:** over MCP a directory is opened as a root only if it is one, never waited on ([1ea6556](https://github.com/this-is-tobi/rta/commit/1ea655672a9d489329d065c673463edf74eb1e4d))
* **git:** over MCP a file of config inside the roots is held to what the repository's config is ([c382c9e](https://github.com/this-is-tobi/rta/commit/c382c9e3247c630c8d61e84dca1da030dc1a66d1))
* **git:** over MCP a gitdir: condition is matched against what the gate judged ([ca6eb03](https://github.com/this-is-tobi/rta/commit/ca6eb037ac1a29fcdced9ee6c31235c616acb666))
* **git:** over MCP a hasconfig condition in the repository matches the repository's remotes alone ([3794be6](https://github.com/this-is-tobi/rta/commit/3794be61ab43b19cd5c41c9cb79da157f9baf2a5))
* **git:** over MCP a repository is read through its directories held open beneath the roots ([97dbb01](https://github.com/this-is-tobi/rta/commit/97dbb01275b89941b15f703d1817fa7cbdfa06d7))
* **git:** over MCP an include and an excludes file the gate judged are read beneath the roots ([3c2338f](https://github.com/this-is-tobi/rta/commit/3c2338f39cec8f3fc75ad26467d12b04e3648a31))
* **git:** over MCP an include the repository makes of a file outside the roots is never opened ([f81fe51](https://github.com/this-is-tobi/rta/commit/f81fe5170448f54b6398d661ba88af123da7c577))
* **git:** over MCP git.hooks lists the hooks directory from the root it lies under, held open ([b773482](https://github.com/this-is-tobi/rta/commit/b773482daf84d60a152d0dffd181c4720626b3c3))
* **git:** over MCP the diff and the status read the working tree from the directory held open ([0b10259](https://github.com/this-is-tobi/rta/commit/0b10259baa7df5da06e04ade85da9070b9e05a35))
* **git:** over MCP the root a relative config path lies under is found from the working directory ([2c37c48](https://github.com/this-is-tobi/rta/commit/2c37c48c30dbc8bd50b222087ad431a5d87858c9))
* **git:** over MCP the walk to a repository looks at each directory from the root it lies under ([b28e49c](https://github.com/this-is-tobi/rta/commit/b28e49c2fad24aed3df6567a965665cb0cb5a687))
* **git:** the overview says an upstream this repository has no ref for is gone, not up to date ([5e29343](https://github.com/this-is-tobi/rta/commit/5e29343901365b7abb163175241bcc86ced3238a))
* **http:** the header input's help reads 'Key: Value' on every surface, without curl's -H ([015d282](https://github.com/this-is-tobi/rta/commit/015d282094e397c8536121ba8eba130f6fde9b39))
* **mcp:** a built-in opens a caller's path from its root, not by the name the gate judged ([e106507](https://github.com/this-is-tobi/rta/commit/e1065079edfd3ee390eff3e2339d376dca50c95c))
* **pathguard:** a link it cannot follow is judged by where it points, not where it sits ([1cfd3ba](https://github.com/this-is-tobi/rta/commit/1cfd3ba2a591ad7321626000fb602586aae6eccb))
* **pathin:** a named pipe put where a walk opens a directory is refused rather than waited on ([d43dceb](https://github.com/this-is-tobi/rta/commit/d43dceb21dc62ab181e62aef373959aacfa03699))
* **plugin:** a built-in's failure with no code or message reaches every surface coded and worded ([a4da709](https://github.com/this-is-tobi/rta/commit/a4da70940595e4becb21dca8822f42c36d03c3ac))
* **plugin:** a forced exit under plugin dev names the command it ran, in the format that asked ([efa5cc3](https://github.com/this-is-tobi/rta/commit/efa5cc3f5ea1fd444b0816b908fb0be00e1530b4))
* **plugin:** a list a call gives is the flag once per element, and the box's comma-separated text ([cfe4c6a](https://github.com/this-is-tobi/rta/commit/cfe4c6aeab1ac68f3d525aa041a7fe61b5ffffa8))
* **plugin:** a load problem keeps its hint, and one every plugin meets is said once ([d05ee97](https://github.com/this-is-tobi/rta/commit/d05ee9784af27c4725d83803ee44699590d22111))
* **plugin:** a nil *view.Error returned as a handler's error is a success, not an empty failure ([cfe5ef9](https://github.com/this-is-tobi/rta/commit/cfe5ef94b9530290ea31ac7ee76fe24447a6b560))
* **plugin:** a plugin failure with no code and no message is named, not shown as a bare ERROR ([43e2123](https://github.com/this-is-tobi/rta/commit/43e2123a8bbddb8273f31e5adc3287947b7d3435))
* **plugin:** a plugin load problem reaches the terminal with what it acts on removed ([8b68582](https://github.com/this-is-tobi/rta/commit/8b68582fc87828afc062aa3b7d8f1c7a3a60ee66))
* **plugin:** a value a call gives by its place that opens on a dash follows --, after every flag ([9121fde](https://github.com/this-is-tobi/rta/commit/9121fde05fdbe5d6f79060862d01980c624b33e8))
* **plugin:** an index add clones beside the attached ones and renames the clone in once checked ([32226f3](https://github.com/this-is-tobi/rta/commit/32226f31771e5fe76845a5f34f17349549dd063b))
* **pluginhost:** a plugin's socket is made in a private directory rta removes with the process ([f6e5433](https://github.com/this-is-tobi/rta/commit/f6e543386aebbda6333db6a73f99639488a91abf))
* **pluginhost:** a plugin's stderr is read by rta and not by go-plugin, whose parser panicked on it ([972748e](https://github.com/this-is-tobi/rta/commit/972748e4f20f114774118f34384ba6b960804fff))
* **pluginhost:** a plugin's stderr reaches no terminal, and its last words end rta's report ([dc0553a](https://github.com/this-is-tobi/rta/commit/dc0553ac2777b541505eb2003fa0dc49655fad8a))
* **pluginhost:** a TMPDIR too long for a plugin's socket is refused by name, with the length to use ([2083ba4](https://github.com/this-is-tobi/rta/commit/2083ba440be9e60abd16d5258ab3d8c9174fefa1))
* **plugin:** install, manifest and upgrade pass a launch's coded refusal on as itself, hint included ([09f4531](https://github.com/this-is-tobi/rta/commit/09f453139c639a5b88496a7363385f1f37503eb2))
* **plugin:** only the system's "certificate is not trusted" is read as an untrusted certificate ([97c505a](https://github.com/this-is-tobi/rta/commit/97c505ac4a5736a164d050bda5049c77563dc1bf))
* **plugin:** plugin dev draws a refusal from the command after -- in the format it asked for ([fba2964](https://github.com/this-is-tobi/rta/commit/fba2964e872ff02d21ecb9da1a8f5b4b92d6e90a))
* **plugin:** plugin dev hands the command after -- the context a signal cancels ([31f129e](https://github.com/this-is-tobi/rta/commit/31f129ec3be111f01aedee7f544d1b70fc8ea40c))
* **plugin:** plugin dev passes a launch's coded refusal on as itself, hint included ([bad6ade](https://github.com/this-is-tobi/rta/commit/bad6ade136a4869397daf20195a4f82027b5bf1b))
* **plugin:** plugin dev's confinement line counts the paths its sandbox denies ([b4c4c56](https://github.com/this-is-tobi/rta/commit/b4c4c568c32bfaeeca7db4ea30e4ae712a8112fb))
* **profile:** profile show says a problem once, under every entry it is about ([31202ab](https://github.com/this-is-tobi/rta/commit/31202ab327c2b5633cad05c9a8b6ef3796c95d14))
* **sdk:** a handler's failure with no code or message leaves the plugin coded and worded ([52281d0](https://github.com/this-is-tobi/rta/commit/52281d08206e7e73016ba563986b0472b8cd809c))
* **sdktest:** the verbs rule counts the words it names as a sentence does, one verb or two verbs ([4264d28](https://github.com/this-is-tobi/rta/commit/4264d28ae492b5124393f09587d714fc5eb7d103))


### Code Refactoring

* **app:** doctor and the index hint take their words from format.Plural, and pick is gone ([ce19719](https://github.com/this-is-tobi/rta/commit/ce19719a987a287c0b3f6e713b3d878c9d4feaea))
* **pluginhost:** a host takes no standard error, since nothing of a plugin's is written there ([c6b3b49](https://github.com/this-is-tobi/rta/commit/c6b3b494a7a14fb805b0fbc8b77ff7902d3213de))

## [0.31.0](https://github.com/this-is-tobi/rta/compare/v0.30.0...v0.31.0) (2026-09-28)


### ⚠ BREAKING CHANGES

* **git:** every git capability refuses a repository whose config sets extensions.compatObjectFormat more than once, where it answered.
* **git:** every git capability refuses a repository whose extensions.compatObjectFormat names the format its objects are already named in, where it answered.
* **git:** the capabilities that read objects refuse a repository whose config sets a format version and extensions.partialClone to nothing, where they answered from the objects it held.
* **git:** the capabilities that read objects refuse a repository whose config sets remote.<name>.partialCloneFilter, where they answered from the objects it held.
* **git:** git.log, git.diff, git.status and the other capabilities that read objects refuse a repository that a promisor set in the operator's config or in git's environment makes a partial clone, and refuse to answer with git.config.unreadable where a file of that config cannot be read, where they answered from the objects held.
* **sdk:** sdktest.Check's spelling rule, and spelling.Find, now hold -o, -y and -h in declared text and sentences the way they hold --output, --yes and --help. A plugin whose text names one of them for rta's command line fails the rule until it is reworded or waived with sdktest.Skip.

### Features

* **sdk:** the speller holds the host's short switches, -o, -y and -h, as it holds their long ones ([f3b94c7](https://github.com/this-is-tobi/rta/commit/f3b94c7fd6e659422f8e61850d2163b2693c1d03))


### Bug Fixes

* **agent:** agent pending --server says how many requests it kept off the queue in words that agree ([9675d2c](https://github.com/this-is-tobi/rta/commit/9675d2c4880e8c844f18d1c8c2e94cd6e0fa96d2))
* **examples:** hello.languages says how it renders without a flag only a terminal has ([a9c71dd](https://github.com/this-is-tobi/rta/commit/a9c71dd59ace98df57d751206b5e6ac8e9c22198))
* **git:** a compatObjectFormat equal to the object format is refused, as git cannot open it ([e5a5442](https://github.com/this-is-tobi/rta/commit/e5a5442236429842d509669fb81dbe69ac11733a))
* **git:** a format extension set on two lines is judged by its last line, as git reads it ([3e1cd00](https://github.com/this-is-tobi/rta/commit/3e1cd002e425bae263f5a7c7339caa83b704d9df))
* **git:** a hook git cannot exec, a directory or a pipe at its name, is listed as fails ([8281d2d](https://github.com/this-is-tobi/rta/commit/8281d2d0a461d61445d663d401a6c99cf3695af2))
* **git:** a hook's status is what access(2) answers, as git asks it, not its execute bits ([a354b4b](https://github.com/this-is-tobi/rta/commit/a354b4be77b07eac1b4e87751230af68cfd14fa7))
* **git:** a partialCloneFilter set for a remote makes a partial clone, as git reads it ([8d1349a](https://github.com/this-is-tobi/rta/commit/8d1349a7dbde3bac685d9549f3e41e27f7d6bce0))
* **git:** a promisor is read from every config git reads, not the repository's alone ([855ce11](https://github.com/this-is-tobi/rta/commit/855ce11afaa8414dd1f1e11ecfcf227979d1968f))
* **git:** extensions.compatObjectFormat set more than once is refused, as git refuses it ([3923c5c](https://github.com/this-is-tobi/rta/commit/3923c5c7a0e1ab4e44c87dfa252487217a5de194))
* **git:** extensions.partialClone set to nothing makes a partial clone, as git reads it ([160d9ff](https://github.com/this-is-tobi/rta/commit/160d9ff98307271660e5f27b1b36c024467eb3d2))
* **kv:** what kv says of a key names it as the prompt does, with hidden characters spelled out ([eb8844a](https://github.com/this-is-tobi/rta/commit/eb8844a683c6ae8acc9f86bd01d5fee9eeaaf4cc))
* **plugin:** a call handed to an agent never spells a value that is not UTF-8 as another value ([bc54458](https://github.com/this-is-tobi/rta/commit/bc544588a30a6c5b2e88f74cc168e7c58d312c16))
* **plugin:** a forced exit during plugin dev removes the build it made, unless --keep asked for it ([ad8bd0c](https://github.com/this-is-tobi/rta/commit/ad8bd0c04836710c81a0e04f0b9eb2ae0be010e7))
* **plugin:** an index remove detaches the clone in one rename before removing any of it ([b629d92](https://github.com/this-is-tobi/rta/commit/b629d92da51623d3a0cf5d6d246ac4f5eea3a9a5))
* **plugin:** plugin dev --keep says on standard error where it left the build ([33e472b](https://github.com/this-is-tobi/rta/commit/33e472be4a8dcb648b4ab4dd3265245a2f8daeca))
* **tui:** the row actions on grant list act on its table when roles in force lead the roster ([c562a55](https://github.com/this-is-tobi/rta/commit/c562a55db67bc70f358e89e1fc4053c6df9d555f))


### Code Refactoring

* **mcp:** the records a ledger row keeps are named as the ones its call names ([1ce9c8d](https://github.com/this-is-tobi/rta/commit/1ce9c8d92266a1ec7521b34d3386e93a47c4d00c))

## [0.30.0](https://github.com/this-is-tobi/rta/compare/v0.29.0...v0.30.0) (2026-09-28)


### ⚠ BREAKING CHANGES

* **audit:** audit web refuses a host with white space around it, one holding a character that draws as nothing, or one that is not ASCII, with audit.web.badhost, and audit mail a domain with white space around it or inside what it names with audit.mail.baddomain, where each audited the value without it or the name IDNA mapped it to.
* **tui:** n on a row of grant list --server opens nothing, where it opened a renew of this machine's grants matching the row.
* **grant:** x on a row of grant list --server refuses with grant.remote.exact when the server runs an rta that does not know --exact, where it took back every grant on the row's target and agent the other cells matched.
* **grant:** grant list --server shows an Artifact column after Capability when a grant's plugin build does not answer on the server, and on every roster read from a server that sends no verdict, as one older than this does.
* **grant:** agent allow --ttl on a call naming a record that is only white space releases the call and issues no grant, and the operator channel's grant.issue refuses such a grant with grant.scope.blank.
* **sdktest:** sdktest.Check fails a plugin whose declared text spells a flag, an rta command line in a code span, or one naming one of its capabilities or its namespace. Name an input as `limit` and a capability by its ID, word anything surface-specific at run time through req.Surface(), or waive the text with sdktest.Skip(sdktest.RuleSpelling, id, why).
* **git:** the capabilities that read objects refuse a partial clone as git.objects.partial, including a blob-less one whose history git.log listed.
* **git:** git.hooks refuses a repository whose last core.hooksPath has no value, and lists the top of the filesystem where it is set to nothing, where it listed another directory's hooks.
* **git:** git.status, git.diff without --commit and git.overview are refused as git.status.timeout where reading the working tree takes more than two seconds; they answered however long it took.

### Features

* **grant:** a remote roster marks each grant's plugin build as the server judges it ([b70650d](https://github.com/this-is-tobi/rta/commit/b70650d3a121ed774cf8db0f267a072ce62623ca))
* **grant:** grant revoke and renew take --exact, and x and n on a roster row act on its grant ([7a14caf](https://github.com/this-is-tobi/rta/commit/7a14caf5dedbfad4d78169a0d607a8ad6d1b5a58))
* **sdktest:** the suite holds what a plugin declares to spell no flag or command line of its own ([c1eb3ce](https://github.com/this-is-tobi/rta/commit/c1eb3ce756dd4b4833833b09b3e113913735a0c6))
* **sdk:** the spelling guard is exported as pkg/sdk/spelling, and rta's own tests run on it ([f65633f](https://github.com/this-is-tobi/rta/commit/f65633ff890c923165a5510cd6d3b64e5a6f8310))
* **view:** a warning beside a whole answer is advisory, and the TUI heads partial only a gap ([bdedd8e](https://github.com/this-is-tobi/rta/commit/bdedd8e668dee1c446ad843a092e62b7becf028a))


### Bug Fixes

* **agent:** rta_grants_active leaves out a grant bound to a plugin build that no longer answers ([32c9b39](https://github.com/this-is-tobi/rta/commit/32c9b39267555949a372e3cf55dc2e88e4638231))
* **audit:** audit web and audit mail refuse a host or domain with white space around it ([bb78472](https://github.com/this-is-tobi/rta/commit/bb7847234c8099ff365e0471c6f9159b2347ac19))
* **git:** a core.excludesFile with no value is named as not applied, as git runs with none ([955438a](https://github.com/this-is-tobi/rta/commit/955438a3e89d5604e3fabe8cd81c5d91500fcbfd))
* **git:** a file a status opened is read no further once the call's two seconds have run out ([37caaee](https://github.com/this-is-tobi/rta/commit/37caaee7f4c7ef7917de219c29ba7350c42b6427))
* **git:** a partial clone is refused up front to the capabilities that read its objects ([19ac9ac](https://github.com/this-is-tobi/rta/commit/19ac9aca66054a98dc7e53effa19196bab1d0f0d))
* **git:** a repository's format is decided as git decides it, and config and hooks read any ([2203338](https://github.com/this-is-tobi/rta/commit/2203338a6e9e1a7efc63d82901428313c131c400))
* **git:** a status ignores what git ignores, matching each pattern with git's own matcher ([f655d1e](https://github.com/this-is-tobi/rta/commit/f655d1e92f2fbc70842f522dbe4dab91fbf44e78))
* **git:** a working tree's status is read for two seconds a call, and refused past them ([4a5192c](https://github.com/this-is-tobi/rta/commit/4a5192c9f85477a9512962a0ee1df2936663f099))
* **git:** config.worktree is read only where the config sets a format version, as git reads it ([6c39a6c](https://github.com/this-is-tobi/rta/commit/6c39a6cc37b026df46869ced8a11630ba50bd4d0))
* **git:** git diff over MCP shows a link's text only when it names places under the roots ([0dafb1d](https://github.com/this-is-tobi/rta/commit/0dafb1d1be1714a238c2026837ccc988ccc8f8fe))
* **git:** git.hooks judges a hook that is a symbolic link by what it leads to, as git does ([8e1815a](https://github.com/this-is-tobi/rta/commit/8e1815a54c7b9d5aeeadc1cb5c9a178c14c0d2d3))
* **git:** git.hooks reads core.hooksPath set to nothing, or with no value, as git reads it ([4893e27](https://github.com/this-is-tobi/rta/commit/4893e27ffa8776d4fa453abc1c3ca21ff232143f))
* **git:** the null device is read as an empty config and an empty excludes file, as git reads it ([d23af2a](https://github.com/this-is-tobi/rta/commit/d23af2af0a7ef70f5b015e8052ef4ef549695c0a))
* **glyph:** the Khitan Small Script filler, which fonts that cover it draw as nothing, is unseen ([563d638](https://github.com/this-is-tobi/rta/commit/563d638a049157a3d1b3059bd6844d4fd90ca6c3))
* **grant:** a remote revoke hands on the revoke of the grant still covering it with its server ([c562b7b](https://github.com/this-is-tobi/rta/commit/c562b7b58cd96dde1888e7301b1e1159c495f780))
* **grant:** a revoke by agent, profile or role alone that matches nothing says what it named ([ec2f664](https://github.com/this-is-tobi/rta/commit/ec2f66478bfcc042e8270a91bd2c6f60eee006ac))
* **grant:** after an exact revoke, the grant still covering its target is named exactly ([aa6f354](https://github.com/this-is-tobi/rta/commit/aa6f35480cf8145b1eb68df5e5872d112754af8a))
* **grant:** no path issues a grant on a record of white space, and no refusal offers one ([df72510](https://github.com/this-is-tobi/rta/commit/df7251041720690a5b34dd677751c82a749659b9))
* **grant:** the note on suppressed grants names a policy file only when it knows one ([2a6fd8b](https://github.com/this-is-tobi/rta/commit/2a6fd8bc9dd5361fdc0007d2a6afa62fa56bd204))
* **grant:** the roster's warnings are advisory, so grant list is never headed partial for them ([280535a](https://github.com/this-is-tobi/rta/commit/280535ac539204613a8af9513d43647046934e5e))
* **keys:** keys restore refuses seed words pasted one to a line, leaving none for the shell ([4a6edb0](https://github.com/this-is-tobi/rta/commit/4a6edb0f76fb5e6bf4ea9b29df40a5b720d656f1))
* **kv:** a value pasted at kv set's prompt that spans lines is read whole, dropped and refused ([0fc50b9](https://github.com/this-is-tobi/rta/commit/0fc50b9a439b6cab7a564c1dc3351550c53bcd75))
* **kv:** kv set's prompt reads a line of any length, and ^D on an empty one ends it ([fd2c56f](https://github.com/this-is-tobi/rta/commit/fd2c56f104b8a036373bd167747f3430a3df12b9))
* **mcp:** an operator's grant issue or revoke keeps the record it named, exactly, in its row ([f132f47](https://github.com/this-is-tobi/rta/commit/f132f4749b5c0663dd0d9d62bca3f459873b14bd))
* **pkg:** a forced exit during a release's checksums download removes the file it wrote ([723a35e](https://github.com/this-is-tobi/rta/commit/723a35e2abae354477d4ab10de496a640939dbce))
* **pkg:** a forced exit during a tool's download removes the directory it staged ([d5f6cf3](https://github.com/this-is-tobi/rta/commit/d5f6cf30d940b70be1002e5e201b1fbdfa6a0d10))
* **pkg:** a release's checksums file is read up to 1 MiB, the most one holds, and no further ([bff49e8](https://github.com/this-is-tobi/rta/commit/bff49e84227d7963dba02a63d0014b23e968e2a6))
* **plugin:** a remove, and each version a prune takes out, lands whole past a forced exit ([c95c4f0](https://github.com/this-is-tobi/rta/commit/c95c4f0c40ca36df7d61cb9632d0543203a444f2))
* **plugin:** an install fetches a signature into its staging directory, which a forced exit removes ([33c3dd7](https://github.com/this-is-tobi/rta/commit/33c3dd74ead768eed770774370b0a50405d8cfd8))
* **pluginhost:** a forced exit ends the plugins of every host, not only the application's ([2470c0c](https://github.com/this-is-tobi/rta/commit/2470c0cb1cce27d6bdfc50c9d43e759e06b7f19e))
* **pluginhost:** a plugin launch is held off a forced exit, and none starts once one has begun ([44d0c5e](https://github.com/this-is-tobi/rta/commit/44d0c5e549492081c892e44043230766a3b6247b))
* **pluginhost:** the declaration cache's key is read up to 4 KiB, and one larger is no key ([a33abe3](https://github.com/this-is-tobi/rta/commit/a33abe3eaf59897184e8728f5979e291448a2936))
* **plugin:** plugin manifest reads its checksums file no further than one may be ([801aa41](https://github.com/this-is-tobi/rta/commit/801aa41dfb7fb609c1962bbae26c5c4336520a55))
* **plugin:** the TUI's spelling of a call names a character a reader does not see by code point ([c14a619](https://github.com/this-is-tobi/rta/commit/c14a6191979976aee3fef54cb242b4dac3eaa09f))
* **policy:** policy show counts the grants its ceiling suppresses as a sentence counts them ([60e4818](https://github.com/this-is-tobi/rta/commit/60e4818d8628ae2301fcd692985d8c4df59cb4d1))
* **stdio:** a passphrase prompt reads a line of any length, and ^D on an empty one is no answer ([22ae700](https://github.com/this-is-tobi/rta/commit/22ae7006a09a145c2213a364cfa3228216bacecb))
* **tui:** a row action that cannot follow a remote roster to its server is not offered there ([26d4d39](https://github.com/this-is-tobi/rta/commit/26d4d39916fc279327fd09a5c49b168b7ead7ad1))
* **tunnel:** a forward that never came up is reported as the timeout, not as the exit it caused ([be6ea1d](https://github.com/this-is-tobi/rta/commit/be6ea1d7ea319176640977ab0d38f4ac58e6d7e3))

## [0.29.0](https://github.com/this-is-tobi/rta/compare/v0.28.0...v0.29.0) (2026-09-28)


### ⚠ BREAKING CHANGES

* **git:** git.status lists what an ignore file holding a line longer than 64 KiB ignores, with a git.status.ignore warning naming it, and git.diff names rather than shows the untracked files under it.
* **git:** in a working tree whose ignore files hold more than 1 MiB or 10000 patterns, git.status lists what the ones past that ignore, and git.diff names rather than shows the untracked files under them.
* **git:** git.config and git.hooks fail, as git.config.failed and git.hooks.failed, in an environment whose GIT_CONFIG_KEY_n or GIT_CONFIG_PARAMETERS sets a key git refuses to read, or whose GIT_CONFIG_PARAMETERS separates two words by a vertical tab or a form feed.
* **agent:** agent log shows a record column before the arguments, in every output format, once a row it shows holds a record, and a ledger line carries a records field and writes a character that draws as nothing as its JSON escape.
* **audit:** the kube audits refuse a namespace with white space around it, or of white space alone, with audit.kube.namespace.invalid, where they audited the namespace without it or the whole cluster.
* **grant:** grant list --detail rows carry an Artifact column after Capability, so in -o json, csv and yaml every later column moves one place along; the compact roster carries the column too while a grant is marked.
* **grant:** grant allow, grant renew and grant revoke no longer trim the record they are given, so one with white space around it names that record; one that is only white space is refused as grant.scope.blank where it meant every record.
* **git:** git.config and git.hooks fail, as git.config.failed and git.hooks.failed, in an environment whose GIT_CONFIG_COUNT or GIT_CONFIG_PARAMETERS git refuses to run with.
* **git:** a repository whose working tree holds a .gitmodules over 4 MiB is refused as git.repository.toolarge on every git capability.
* **git:** a repository whose index is over 128 MiB, packed-refs over 64 MiB, config over 4 MiB, or HEAD or a loose ref over 1 MiB is refused as git.repository.toolarge on every git capability, and an operator config file over 4 MiB fails git.config and git.hooks.
* **git:** a line git blame has not traced within two seconds carries the commit its walk had reached, with a hash prefixed by ^ and a git.blame.partial warning, where the walk ran for as long as the history took.
* **agent:** agent show, agent pending and agent log show an element of a list argument holding white space, or a character that draws as nothing, quoted with that character named by its code point, where they printed the list with the element as it is.
* **grant:** grant list, grant issue and grant revoke show a record named "any", or "—" in the plan grant issue prints, in quotation marks.
* **grant:** grant list, grant allow, grant issue, grant revoke and the core.grant.required refusal show a record holding white space, or a character that draws as nothing, quoted with that character named by its code point, where they showed it as it is.
* **agent:** agent pending, agent show, agent allow, agent deny and agent log show a record or a string argument holding white space, or a character that draws as nothing, quoted with that character named by its code point, in every output format, where they showed it as it is.
* **agent:** agent show, agent allow and agent deny refuse a request id with white space around it with agent.request.unknown, where they trimmed it and answered the request under the id without it.
* **pkg:** pkg upgrade refuses a target with white space around it with pkg.upgrade.unknown, where it trimmed the white space and upgraded the manager or the tool named without it.
* **net:** net dns refuses a name with white space around it with net.dns.badname, and net hosts toggle refuses a hostname holding white space, a control character or a "#" with net.hosts.badhostname, where both trimmed the white space and acted on the name without it.
* **kv:** kv set and kv rename refuse a key name with white space around it, including a no-break space, with kv.set.padded, where they wrote the name with the white space trimmed. kv rename looks up the key to move as given, so a key given with white space around it is not found.
* **plugin:** a plugin whose input declares Required beside an empty Default, "" or an empty list, fails to load; drop the Default.

### Features

* **doctor:** doctor warns of a grant bound to a plugin build that no longer answers ([4484d10](https://github.com/this-is-tobi/rta/commit/4484d107e2fdd7bae78d0fb37d0a24fc84355830))
* **grant:** grant list names the plugin build each grant is bound to, and marks one replaced ([95e2731](https://github.com/this-is-tobi/rta/commit/95e27314700d77ddf8a1e44a364f00acfb8dba0e))
* **kv:** kv set at a terminal asks for the value, without echoing it, when given none ([13af725](https://github.com/this-is-tobi/rta/commit/13af725182b082ff76e632fad0bb14532002b28c))


### Bug Fixes

* **agent:** a list argument shows each record in it as the gate compares it ([dd29380](https://github.com/this-is-tobi/rta/commit/dd2938022555d86bd426decd00a8f76c73cf8199))
* **agent:** a parked call's record is shown as the gate compares it, quoted when it is padded ([a441c8e](https://github.com/this-is-tobi/rta/commit/a441c8eb0cf5ed975e2927028f7721365a633450))
* **agent:** a role line naming a folder is offered for a parked call on a record under it ([709e6e7](https://github.com/this-is-tobi/rta/commit/709e6e767081004c8efd2a9df34e9472c51c0076))
* **agent:** agent show, allow and deny take a request id exactly as given ([2a02ee7](https://github.com/this-is-tobi/rta/commit/2a02ee78154d63707d84f9983a68a7a58b812486))
* **agentlog:** its errors name the agent log as a thing, never in the agent.log command's words ([50eeb0e](https://github.com/this-is-tobi/rta/commit/50eeb0eed0445f00e1f19e1db8f88a1201f214c7))
* **agent:** the ledger keeps the records a call was judged on, and agent log shows them ([4c745db](https://github.com/this-is-tobi/rta/commit/4c745db054a2ecf9ac0c9fca48d881bdfe700742))
* **atomicfile:** a streamed write holds off an exit for its rename, not while the stream arrives ([b992295](https://github.com/this-is-tobi/rta/commit/b99229542afc8e66f13700d52e825883268f4e26))
* **audit:** a kube audit refuses a namespace with white space around it, and one of white space ([384384d](https://github.com/this-is-tobi/rta/commit/384384d2a21cb1ee3910e07ff47ca4a448379de8))
* **audit:** audit clients --fix names a VS Code input first for a credential in its mcp.json ([f6ef14b](https://github.com/this-is-tobi/rta/commit/f6ef14b229a924795acbe75607db2c04f8c6c991))
* **audit:** audit clients finds a Gemini CLI server declared with httpUrl, and grades it there ([e1b8a59](https://github.com/this-is-tobi/rta/commit/e1b8a59f3161b587f5992fb09c1c80f4f24cf2d3))
* **audit:** audit clients grades a credential named by a reference its client expands as not held ([1661a15](https://github.com/this-is-tobi/rta/commit/1661a151993d5a4420f5b1f0617b57764ee8b733))
* **audit:** audit clients grades a Gemini CLI %VAR% by the system its client runs on ([10d30b1](https://github.com/this-is-tobi/rta/commit/10d30b1ce361b5edffa3ab9c26368abf80b7c962))
* **audit:** audit clients warns when a Claude Code remote server names a variable it reads as empty ([9344940](https://github.com/this-is-tobi/rta/commit/934494094ca94f8ae65ec14361c711861d70a664))
* **cli:** a command that ignores SIGINT or SIGTERM is exited three seconds on, with 130 or 143 ([00f32a1](https://github.com/this-is-tobi/rta/commit/00f32a1f6f3b1be33d4d02ef7326fa1efb107283))
* **cli:** a forced exit inside a prompt starts its error on a line of its own wherever stderr goes ([4c02c5d](https://github.com/this-is-tobi/rta/commit/4c02c5d5cfb2c8773333249db15339f537cf1191))
* **doctor:** doctor names a grant's record as the gate compares it ([d4c6e71](https://github.com/this-is-tobi/rta/commit/d4c6e7176554a595e906f3c49c70eb7c069ee5a1))
* **fs:** fs tree over MCP tells a link's target only when it names places under the roots ([554fad9](https://github.com/this-is-tobi/rta/commit/554fad92872b2409d4fc0d7902ff400c4bed7576))
* **git:** a .gitmodules past 4 MiB is refused before go-git reads it whole ([48acefe](https://github.com/this-is-tobi/rta/commit/48acefe38b4aa589ec4981860f20b2ee0fa7c8dc))
* **git:** a bare repository's commit diff withholds rta's own state where git checks it out ([34853ba](https://github.com/this-is-tobi/rta/commit/34853bac49cd1db52ce75fcb05d522bb48245fc6))
* **git:** a blame stops comparing a commit's tree with its parents' at the walk's deadline ([a6d4c54](https://github.com/this-is-tobi/rta/commit/a6d4c54d149d89d170dc2fcf931efdc0a72dbb17))
* **git:** a checkout whose core.worktree names another directory withholds rta's state there ([1b3006f](https://github.com/this-is-tobi/rta/commit/1b3006fe09803171564708ec2745f0d03fb83e2d))
* **git:** a commit's diff reads at most 64 MiB looking for renames, and pairs the rest by hash ([43ce330](https://github.com/this-is-tobi/rta/commit/43ce330913b7cfc96d0bd0c04358596b628778a0))
* **git:** a diff looks at no more than 10000 files, and counts the ones it did not ([62b0a1d](https://github.com/this-is-tobi/rta/commit/62b0a1d6fbe51508b9af11746ecc09dca1c6f03a))
* **git:** a diff spends at most two seconds matching lines, and names each file it diffed coarsely ([76d527f](https://github.com/this-is-tobi/rta/commit/76d527f6bd6c117ee305559eca296ee832bb3274))
* **git:** a log of no commits is empty, and an empty status, log, branch list or blame says why ([4285716](https://github.com/this-is-tobi/rta/commit/4285716ecf9560acd22184c6c5d3613a04e6bba6))
* **git:** a repository file go-git reads whole is refused past a bound, before it is read ([830e741](https://github.com/this-is-tobi/rta/commit/830e74122bca3424ad49da9bf8ba9c576b221389))
* **git:** a repository in a format go-git cannot read is refused as unsupported, naming why ([9607ddc](https://github.com/this-is-tobi/rta/commit/9607ddc8f94dc24e4ba7f6aa397b90c86afd9220))
* **git:** a sparse checkout opens, and its config.worktree is read as the worktree scope ([b17ebbb](https://github.com/this-is-tobi/rta/commit/b17ebbbd4c89c5b975cae777e4eb67d6dd98eafa))
* **git:** a status applies at most 1 MiB and 10000 patterns of ignore files, and names the rest ([c1e66da](https://github.com/this-is-tobi/rta/commit/c1e66da1050b2e900dc21966eacb99ed33107060))
* **git:** a status ignores what core.excludesFile and info/exclude ignore, as git does ([8c3e46c](https://github.com/this-is-tobi/rta/commit/8c3e46c82598df5e188b5dcb78084efdaaecb3ea))
* **git:** an ignore file is read as git reads it, and one with a line go-git stops at is named ([8e1b8de](https://github.com/this-is-tobi/rta/commit/8e1b8de9f7e02136eff0ebb7524b03fd4ae3b3de))
* **git:** git blame in a bare repository puts its file to the gate where git checks it out ([285cea0](https://github.com/this-is-tobi/rta/commit/285cea0cce2477ae00571cb8b3a8f4fc317cf219))
* **git:** git blame spends at most two seconds on a history, and marks the lines it did not trace ([d0f9451](https://github.com/this-is-tobi/rta/commit/d0f9451eed8e91b7de15635933c39d0a36cdbe19))
* **git:** git.config and git.hooks read every system file git's builds read, as its environment says ([2e5189e](https://github.com/this-is-tobi/rta/commit/2e5189e84bfaa622f506c3fb07ec9b407069543d))
* **git:** git.config and git.hooks read the config git's environment sets for one command ([bce00bd](https://github.com/this-is-tobi/rta/commit/bce00bd5503b62a353839c34bbf4bb99449f9e0d))
* **git:** git.status marks a path whose kind changed T, as git status does ([65ad35a](https://github.com/this-is-tobi/rta/commit/65ad35a77954a4e03676eddcf6707dc4add23f1f))
* **git:** rename detection stops at git's limit of files and at its share of the call's time ([905fd9e](https://github.com/this-is-tobi/rta/commit/905fd9eeace7f63c22eef4542c892cad5d3812bf))
* **git:** the command scope is read as git reads it, its count, its white space and its keys ([3dca901](https://github.com/this-is-tobi/rta/commit/3dca901f5d914c9e83716381f908c70a425a13c6))
* **git:** with core.ignorecase set, a status matches ignore patterns in either case, as git does ([aa2a690](https://github.com/this-is-tobi/rta/commit/aa2a69085f83f1970800c57dd2d07a1519ea3315))
* **grant:** a held record holding a tab is not offered to revoke or renew as another record ([6a19790](https://github.com/this-is-tobi/rta/commit/6a1979052a7282441586941b7d7a8a09af2e0986))
* **grant:** a held record offered to revoke or renew is described as the gate compares it ([62eddea](https://github.com/this-is-tobi/rta/commit/62eddea2f6d8db0215bc5d103754cfe7756d8926))
* **grant:** a record spelled like the roster's word for none is shown quoted ([ffc8492](https://github.com/this-is-tobi/rta/commit/ffc8492c94e8cd0ca68c67a14f646b0d24b1ec60))
* **grant:** grant allow, renew and revoke take a record as given, and refuse white space alone ([3d692c5](https://github.com/this-is-tobi/rta/commit/3d692c53ed03fc492885feefd6ef4438e9716a08))
* **grant:** grant list and a grant refusal show a record as the gate compares it ([b20deed](https://github.com/this-is-tobi/rta/commit/b20deed7d468ae3f4c0fc9ef882511673a6d3ee6))
* **grant:** one grant, and one operator key, is counted in the singular ([76a7651](https://github.com/this-is-tobi/rta/commit/76a7651efcd4e783fd65dc8239755fafd9888ec0))
* **keys:** a key pair keys add or keys restore writes lands whole past a forced exit ([8cec1ae](https://github.com/this-is-tobi/rta/commit/8cec1aed63849b80856d2434eb5d66d6f3e6628b))
* **kv:** a value piped through --file /dev/stdin is labelled by what it holds, as piped ([1fa8f89](https://github.com/this-is-tobi/rta/commit/1fa8f89648c941f2523d5a8bbf43ed2e6e2b0ef0))
* **kv:** kv set and kv rename refuse a key name with white space around it ([f766f5b](https://github.com/this-is-tobi/rta/commit/f766f5b3b68cd0b3edc0d9959ac084c016cfa5e9))
* **mcp:** a link the caller named is described only by names under the server's roots ([0c0a1dd](https://github.com/this-is-tobi/rta/commit/0c0a1dded521346eb5ded1e7aa87e01414a17042))
* **mcp:** a tool's schema publishes no empty default ([65aff09](https://github.com/this-is-tobi/rta/commit/65aff09c161608f0ead8810c25add0a068ac9431))
* **mcp:** a value of the wrong shape is refused naming the "x" argument, as the tool's schema has it ([3f389fd](https://github.com/this-is-tobi/rta/commit/3f389fd5545ae3aa2116fc2376a6180da9927421))
* **mcp:** an option miss is hinted naming the "x" argument, as its refusal names it ([e310c19](https://github.com/this-is-tobi/rta/commit/e310c192bfc4a98d3f2e6fb627f290775bdd2e2d))
* **mcp:** serve --help names what --http hides from the list the locality gate reads ([bfe73e4](https://github.com/this-is-tobi/rta/commit/bfe73e4046109bc32ce47d3ed3206e15baf0a219))
* **net:** a hosts or resolver backup is held to the cap the read before it was ([7d5b01f](https://github.com/this-is-tobi/rta/commit/7d5b01f46cc71e226c315afd8ee8e753561e1e3b))
* **net:** a proxy value is masked up to its last @, past a password's raw /, ? or # too ([00b74c9](https://github.com/this-is-tobi/rta/commit/00b74c9ac1f697e50d2c022f9cc6fdda9c069027))
* **net:** net dns and net hosts toggle refuse a name with white space around it ([888dab3](https://github.com/this-is-tobi/rta/commit/888dab34c3ba4cc54fb2d358ec9d3765ebc799bf))
* **net:** the overview reads resolv.conf as a file, to net.resolver.list's cap ([3d73289](https://github.com/this-is-tobi/rta/commit/3d732890c3f3e01355b6f577cde3ff0eb5f7eab8))
* **pathguard:** a path the handler reached is refused without asking the caller for another ([c6c8fa5](https://github.com/this-is-tobi/rta/commit/c6c8fa5c71d9863d86cc7bc5e2c4c5515e4b8b92))
* **pkg:** pkg upgrade takes its target as given, and one with white space around it is unknown ([87dc4c9](https://github.com/this-is-tobi/rta/commit/87dc4c9ca8b3d9f676d118565fefcb4b44d2f725))
* **plugin:** a call spelled for the CLI joins a switch turned off to its flag, --tls=false ([10e161d](https://github.com/this-is-tobi/rta/commit/10e161dfee18373ee88819eb643ebb5d31914af6))
* **plugin:** a forced exit during an install's fetch removes the download it staged ([ed345d6](https://github.com/this-is-tobi/rta/commit/ed345d6b2cbb83f1a021ed451d7e2c5f3ff85447))
* **plugin:** a missing input names the call as the caller's surface spells it ([9ebe99e](https://github.com/this-is-tobi/rta/commit/9ebe99ea600260ed4925c85763e3b676c82be209))
* **plugin:** a required input declares no default at all, an empty one included ([42e758d](https://github.com/this-is-tobi/rta/commit/42e758d1e85619444d3cf05d01eb9a8cffa9b6a6))
* **plugin:** a shape, option or range refusal names the call as the caller's surface spells it ([d9c8f4c](https://github.com/this-is-tobi/rta/commit/d9c8f4c9b4d8d38883c3fa931eeb59c8858c3aa0))
* **plugin:** an install's placement, trust and lock entry land together past a forced exit ([438b4b9](https://github.com/this-is-tobi/rta/commit/438b4b9676683d26a9fa230481162b6734958dd1))
* **pluginhost:** a declaration cache entry is read up to 16 MiB, and one larger is a miss ([a64e36e](https://github.com/this-is-tobi/rta/commit/a64e36ec74af0f7a790baa538c768a9ba81e439c))
* **pluginhost:** a declaration cache entry is written through atomicfile, held off a forced exit ([076612a](https://github.com/this-is-tobi/rta/commit/076612a0c3750d3b7ceec19c11c1b9c17c10a382))
* **shellquote:** a character that draws as nothing is spelled by its bytes in a command shown ([2ad742e](https://github.com/this-is-tobi/rta/commit/2ad742e59ca703d183ab25685b67e06ffbc22182))
* **textclean:** a record ending in a Braille blank or a null notehead is shown quoted ([838e7ee](https://github.com/this-is-tobi/rta/commit/838e7eecbb4cce7cc1fb4f7133676aa742a3f877))
* **tui:** x and n on a grant row seed the record and connection the grant holds ([764d86a](https://github.com/this-is-tobi/rta/commit/764d86aadac83e0979bd59fd7448f4450f71309b))


### Performance Improvements

* **atomicfile:** a capped read takes the memory the file holds, not the memory its cap allows ([a963560](https://github.com/this-is-tobi/rta/commit/a9635607201bcd70cdb1b842192b6a537b2374cb))
* **git:** a call keeps up to 50 packfiles open while it reads, and closes them when it returns ([4d5bf7c](https://github.com/this-is-tobi/rta/commit/4d5bf7c49131ddb90498085878290402431caf33))
* **git:** a working tree's diff and status read each directory once, and the index by halves ([d5e471e](https://github.com/this-is-tobi/rta/commit/d5e471eb5d6079eab53e075a58f250fb2c2e53a7))


### Code Refactoring

* **shutdown:** store writes and an edit's plaintext hold off an exit, and an editor has the tty ([31cc758](https://github.com/this-is-tobi/rta/commit/31cc758f4988971415be72e08509300383e14f06))


### Dependencies

* the binary size ceiling is 50 MB, and AGENTS.md states it ([a38e708](https://github.com/this-is-tobi/rta/commit/a38e708c1e1c6a1619a50ab4e9567bc63ec412ef))

## [0.28.0](https://github.com/this-is-tobi/rta/compare/v0.27.0...v0.28.0) (2026-09-27)


### ⚠ BREAKING CHANGES

* **plugin:** plugin index update exits non-zero when an index it pulled holds no manifest rta can read, with plugin.index.empty, or with the code of the first manifest it could not parse when every one it holds is malformed.
* **grant:** a call naming a record with white space around it is no longer covered by an exact grant on the trimmed name, and asks for its own.
* **git:** git blame refuses a history it would read more than 64 MiB of in all with git.blame.toolarge, where it blamed any history whose every version and every step stayed within bounds.
* **audit:** audit deps and audit why refuse a manifest named on its own that is over 64 MiB with audit.deps.toolarge, and one that is a named pipe, a socket or a device off the CLI with audit.deps.notafile. audit clients grades no agent config over 64 MiB, and names it as one it could not read.
* **plugin:** a plugin declaring a list argument that is not the last the command line takes fails to load. Make the argument after it a flag, or the list the last argument.
* **cli:** a capability declaring an optional argument before a required one takes the required one first. `rta git blame <repo> <file>` is now `rta git blame <file> <repo>`, and a plain `rta git blame <file>` blames the file in the current directory's repository.
* **plugin:** a plugin declaring an input both Required and with a non-empty Default fails to load. Drop Required to keep the default, or drop the Default to keep it required.
* **fs:** fs hash with an expect holding no checksum, such as the prefix sha256 and its colon alone, is refused with fs.hash.expect instead of hashing the file without comparing it.
* **net:** net hosts toggle on a name parked at more than one IPv4 address, or at more than one IPv6 address, is refused with net.hosts.ambiguous instead of enabling every entry.
* **net:** net hosts add, rm and toggle on a hosts file that is a symbolic link are refused with net.sysfile.managed instead of replacing the link with a regular file.
* **builtin:** cert, fs hash and the net hosts and resolver capabilities refuse a named pipe, a device or a directory everywhere but the CLI, with cert.file.notafile, fs.hash.notafile or net.sysfile.notafile. A certificate file over 16 MiB, a hosts file over 32 MiB and a resolv.conf over 1 MiB are refused on every surface, with cert.file.toolarge or net.sysfile.toolarge.
* **git:** run from inside a checkout's .git directory, the git capabilities open that directory as the repository, with no working tree, as git does, rather than the checkout around it.
* **git:** git.blame is refused as git.blame.toolarge for a file larger than 16 MiB at HEAD or in its history, or one added or renamed in a commit that changed more than 64 MiB beside it, rather than answered.
* **git:** git.hooks rows carry a third column, Path. Over MCP, a hooks directory outside the server's roots, as a core.hooksPath in the operator's own config usually names, is refused as core.mcp.path.outside instead of the repository's unused hooks being listed.
* **git:** from a terminal outside the checkout, rta git blame and rta git log --file take a relative file from the current directory rather than the repository root, so `rta git blame repo README` is now `rta git blame repo repo/README`.
* **git:** over MCP, a linked worktree or a submodule checkout whose git directory is outside the server's roots is refused as core.mcp.path.outside. Serving a root that also holds the main checkout opens it as before.
* **audit:** audit web refuses a host argument with no host name in it, such as a bare `:8443` that used to audit this machine, as audit.web.badhost; name the host, localhost:8443.
* **audit:** the kube audits refuse a namespace the cluster does not have with audit.kube.namespace.notfound, where they answered a report about it, and each narrowed run makes one namespace lookup more.
* **audit:** audit deps and audit why refuse a file named on its own that is no manifest they read, as audit.deps.format, where they answered a report with no dependencies in it.
* **audit:** audit mail refuses with audit.mail.resolver when every lookup it made failed, where it answered a report of failed rows graded ok.
* **audit:** audit web refuses a host argument that carries userinfo, with or without a scheme, as audit.web.badhost; pass the host after the @.
* **agent:** agent allow with both --role and --server is refused and answers nothing; answer the call, then issue each of the role's lines with grant allow --server.
* **agent:** agent allow --ttl on a record ending in a slash releases the call and issues no standing grant; grant allow issues the folder deliberately.
* **http:** http.* refuses, on every surface, a destination in shared address space, a Tailscale peer's included, or in 0.0.0.0/8, 192.0.0.0/24, 198.18.0.0/15 or 240.0.0.0/4, a multicast or site-local one, one in the local-use NAT64 prefix, and an IPv6 address carrying an IPv4 one it refuses.
* **grant:** a folder grant no longer covers a call whose record holds an escaped, doubled, backslashed, overlong, lookalike or padded dot segment, and grant allow refuses a folder scope holding one; such a record is reachable through an exact grant.
* **kv:** kv env with two keys whose variable names coincide exits with kv.env.collision instead of printing both lines. Rename one of the keys, or export them in separate calls with different prefixes.
* **plugin:** an allow list in the system root's trusted.json is ignored, and a stored grant naming a location the plugin does not declare opens nothing. Run rta plugin allow for each plugin that should read a credential location, or set RTA_ALLOW_PLUGINS on the image.
* **kv:** a kv.rename grant naming a single key no longer authorizes moving it to a name the grant does not cover. A folder grant covers moves inside the folder, and a grant for one key needs a second one naming the new name.
* **kv:** kv_show over MCP no longer carries a copy row, and its reveal and history rows hold the kv_get and kv_history calls an agent makes instead of `rta` command lines. The CLI's kv show keeps every row as it was.
* **fs:** fs tree with detail keys its not-shown rows "below depth" and "past limit" instead of "below --depth" and "past --limit". A script reading them from -o json or from the fs_tree tool reads the new keys.
* **init:** rta init writes a key/value view to stdout in the format -o asks for once the form is closed, where it wrote "✓ wrote <file> — run `rta` to see your dashboard" or "nothing written". A script that read that line should read the "wrote" or "unchanged" pair of -o json.
* **mcp:** rta mcp install writes a key/value view to stdout in the format -o asks for, where it wrote "✓ registered with <client>", "would run <command>" or "Add this to <file>" and the block, and the client's own command now writes to stderr rather than stdout. A script that read the block should read the "block" pair of -o json. A default output format that names none refuses it, exit 2, before the client's command runs.
* **plugin:** rta plugin new writes a key/value view to stdout in the format -o asks for, where it wrote "Created <dir>" and a page of steps, or "would write N files in <dir>" and a list under --dry-run. A script that read that text should read the "created" pair of -o json, which names the directory by its full path, and the "files" pair. A default output format that names none refuses it, exit 2, before it writes anything.
* **plugin:** rta plugin untrust writes a key/value view to stdout in the format -o asks for, where it wrote "withdrew N approvals — ..." or, for --all with nothing to withdraw, "no plugin artifact is trusted". A script that read that text should read the "untrusted" and "approvals" pairs of -o json. A default output format that names none refuses it, exit 2, before it withdraws anything.
* **policy:** rta policy require writes a key/value view to stdout in the format -o asks for, where it wrote "✓ <file> now requires a .rta-policy.yaml" and a line about this directory, on stderr when the directory had none. A script that read that text should read the "wrote" and "this directory" pairs of -o json. A default output format that names none refuses it, exit 2, before it writes anything.
* **policy:** rta policy init writes a key/value view to stdout in the format -o asks for, where it wrote "✓ wrote .rta-policy.yaml" and two lines of prose. A script that read that text should read the "wrote" pair of -o json, which names the file by its full path. A default output format that names none refuses it, exit 2, before it writes anything.
* **grant:** with the guard orphaned, `rta grant list` without --detail answers -o json and -o yaml with {"type": "table", "rows": []} and a warning coded core.grant.guard.orphaned, where it answered {"type": "text", "body": "guard  ORPHANED — ..."}, and -o csv writes the header row with the warning as a "#" note. A script that read `.body` should test for that code in `.warnings` instead.

### Features

* **plugin:** a hint names a positional input by its slot, and a call with its inputs, per surface ([183083e](https://github.com/this-is-tobi/rta/commit/183083e27ce2330c38e5315fdf48947aa7c209c0))
* **plugin:** a message names a capability and an input as its caller's surface spells them ([3954a0e](https://github.com/this-is-tobi/rta/commit/3954a0e77e8257f8432e1feff9951af8d16ae1bf))
* **plugin:** a page hands over a whole call, values and all, spelled for the surface asking ([023c4cf](https://github.com/this-is-tobi/rta/commit/023c4cf7c23ca1ea046c2b96c37c497305867c4d))
* **plugin:** a placeholder in a call the SDK spells stays bare, as a usage line writes it ([e71a56c](https://github.com/this-is-tobi/rta/commit/e71a56cd1ff0272cb1eaeb29e27cbced4a915b1a))


### Bug Fixes

* **agent:** a parked call naming several records is offered a role with a line for each ([96409b7](https://github.com/this-is-tobi/rta/commit/96409b74e9c82eef28398c0c2d3efba48f35d52c))
* **agent:** agent allow --ttl issues no grant on a record that is a folder, and says how to ([4f5f12a](https://github.com/this-is-tobi/rta/commit/4f5f12a63156de9f14b799ea6eeaf43e5d0b39e9))
* **agent:** agent allow given a server refuses --role as it refuses --ttl, rather than dropping it ([cfd1782](https://github.com/this-is-tobi/rta/commit/cfd17824d00ebf37eb51a74222826d243731356b))
* **agent:** agent allow holds each record a call names to the ceiling, and --ttl grants each ([8316101](https://github.com/this-is-tobi/rta/commit/8316101402b5bee52f79f69dd1836bfb07d54ade))
* **agent:** the consent pages name the calls that answer them as the surface showing them ([e427f38](https://github.com/this-is-tobi/rta/commit/e427f380d0515b81d8ba1d82eb345caf01b6db91))
* **audit:** a dependency walk bounds the chains it queues, not only the ones it takes ([c2920d6](https://github.com/this-is-tobi/rta/commit/c2920d63183dd39d35410329f776fd9aab97e865))
* **audit:** a kube audit narrowed to a namespace that does not exist refuses, not reports ([5c0b960](https://github.com/this-is-tobi/rta/commit/5c0b960b3f0f3a880925cd713ea8afddf1a915dc))
* **audit:** a package with a long dependency list is read in time linear in its length ([78d2514](https://github.com/this-is-tobi/rta/commit/78d2514bd5f53c7f08c6d4db1f2109d526d55b2e))
* **audit:** a pnpm v5 package with a peer suffix is read as the package and version it is ([c0d9296](https://github.com/this-is-tobi/rta/commit/c0d929688925741263bf7f8e129de1b73ae9b297))
* **audit:** a TOML lockfile's subtables no longer mark the package they belong to as local ([212ea25](https://github.com/this-is-tobi/rta/commit/212ea2590ef62dd494ef18a12f66a50d1dc0a45c))
* **audit:** an advisory GitHub grades MODERATE is graded medium ([c484183](https://github.com/this-is-tobi/rta/commit/c4841835f9c9939913cff849f9896e32051b66b1))
* **audit:** an audit finding or hint names its inputs and capabilities as the caller has them ([b15ffb6](https://github.com/this-is-tobi/rta/commit/b15ffb60fcd0327b26c17c56d54480c3e573596f))
* **audit:** an audit names the checks that could not run in its overall, never "no issues found" ([5b0a278](https://github.com/this-is-tobi/rta/commit/5b0a278b381f751178db392a4cde727fd597169c))
* **audit:** an offline, narrowed or selector-less audit counts what it skipped in its overall ([6168d56](https://github.com/this-is-tobi/rta/commit/6168d56808422a939834edc84ed5d60718097809))
* **audit:** audit clients grades every server a file declares, two sharing a name included ([462c1bd](https://github.com/this-is-tobi/rta/commit/462c1bd19d0be53fd6b9033f65b43d955e3e436b))
* **audit:** audit clients grades the servers in Codex's config.toml as it grades JSON ones ([ba2b6d4](https://github.com/this-is-tobi/rta/commit/ba2b6d44a45f051f69403de3d512943e6950b80d))
* **audit:** audit clients reads npm exec, uv tool run and yarn dlx as the runners they are ([4c89fc5](https://github.com/this-is-tobi/rta/commit/4c89fc526feed0448080cf449d8966ecfc86194f))
* **audit:** audit clients reads the package each runner launches, and pins it in its own syntax ([a9c8103](https://github.com/this-is-tobi/rta/commit/a9c81039f3bf2b72b90b59014cda208c4dd075c8))
* **audit:** audit deps asks OSV about the module a go.mod replace builds, not the one it requires ([41dbba3](https://github.com/this-is-tobi/rta/commit/41dbba39e1028cdcd634384029153afa085a708c))
* **audit:** audit deps reads a named requirements file by any name, and refuses one it cannot read ([a5d9260](https://github.com/this-is-tobi/rta/commit/a5d9260e43209a7f5df80930ce1f771eb984df90))
* **audit:** audit deps reads an aliased npm dependency as the package it installs ([ab33e7b](https://github.com/this-is-tobi/rta/commit/ab33e7b1e2f8304ab04d707e572ae50f7f902875))
* **audit:** audit deps reads every level of a v1 package-lock, nested copies included ([795b70f](https://github.com/this-is-tobi/rta/commit/795b70f4f1b94e8a1ee82d1329aff8a592f78ced))
* **audit:** audit deps, why and clients read a manifest or an agent config only as a regular file ([8fa9605](https://github.com/this-is-tobi/rta/commit/8fa9605fccb570af32f3b43ed5ba1f5379d2b783))
* **audit:** audit deps' next step says what a full scan adds, not that the rows lack a severity ([e3ac0bb](https://github.com/this-is-tobi/rta/commit/e3ac0bb346e635dd637fdd5c6feefa38a4652f79))
* **audit:** audit web grades a host that speaks only TLS 1.0, 1.1 or a broken cipher ([2d1cad8](https://github.com/this-is-tobi/rta/commit/2d1cad8a51903775ad857112a92e243b647014ae))
* **audit:** audit web reads an HSTS max-age quoted or spaced as RFC 6797 allows ([e3daf8b](https://github.com/this-is-tobi/rta/commit/e3daf8b5b309558918d90f562275e3172d06663e))
* **audit:** audit web refuses a host with credentials before it, as audit mail does ([6ebc021](https://github.com/this-is-tobi/rta/commit/6ebc02137aafe86a730490c6ce6cd75624f166aa))
* **audit:** audit web refuses a value that names no host, rather than requesting it as it stood ([930a330](https://github.com/this-is-tobi/rta/commit/930a3308e2c5d9cb79ff73818840fb8839f53658))
* **audit:** audit web warns on a Via header only when it names a product's version ([ce62ee9](https://github.com/this-is-tobi/rta/commit/ce62ee9b6708d577229a9c2d29980518d94ca408))
* **audit:** audit web's csp and exposure rows read every line of a header sent twice ([24abfc4](https://github.com/this-is-tobi/rta/commit/24abfc47d83de82ed076932f507e1beebe5ef684))
* **audit:** audit web's framing row counts a header only when it keeps some site out ([ca505ac](https://github.com/this-is-tobi/rta/commit/ca505acd1e65b8f87ebd76b9f8ae5a8a0f7b8e34))
* **audit:** resolving a package-lock's declarations spends a budget, not the file's depth ([c2f41ce](https://github.com/this-is-tobi/rta/commit/c2f41ce398c112280864f0bc8bbfd3d91ceec447))
* **audit:** the copies of a package are counted once per inventory, not once per affected package ([c278117](https://github.com/this-is-tobi/rta/commit/c278117ae34e39a1b193fef52865faf9620a681b))
* **builtin:** a named file is read only up to its format's cap, and off the CLI only if regular ([3a110f1](https://github.com/this-is-tobi/rta/commit/3a110f1a0955fb05f739a278e64d01b06b50f99e))
* **cert:** cert and net probe inspect a host that speaks only TLS 1.0 or 1.1, and name its protocol ([b06833c](https://github.com/this-is-tobi/rta/commit/b06833ce74ded3744e4f3d8288f138d03c7a044f))
* **cert:** cert pem's refusals name include as an input, not as a flag ([ce58971](https://github.com/this-is-tobi/rta/commit/ce589713c5e5473b3cbd52563e80de106c806e80))
* **cli:** -o md draws an error or warning code as it is, in a span nothing in it can close ([593ca05](https://github.com/this-is-tobi/rta/commit/593ca0546bf84c7dea27386b801a932e5805ef76))
* **cli:** arguments bind in the order the usage line shows, required ones first ([445fc54](https://github.com/this-is-tobi/rta/commit/445fc54b77a6cc289b6b61a549c37f75aec8210d))
* **codec:** a hint sends an agent to the codec_jwk tool, not to `rta codec jwk` ([3aca2d0](https://github.com/this-is-tobi/rta/commit/3aca2d09f3f49a921a0c0528d505964f39fc9616))
* **eol:** a range's upper bound takes every cycle under it, so python ..3 includes 3.13 and 3.8 ([77119d7](https://github.com/this-is-tobi/rta/commit/77119d7eedb247891d3978b824218831d79836c9))
* **eol:** an eol hint names eol.products and its inputs as the surface asking calls them ([56a8f69](https://github.com/this-is-tobi/rta/commit/56a8f697c1f8f4cadb67c2ea328763a00ba514cf))
* **fs:** fs hash refuses an expect that holds no checksum rather than skipping the comparison ([45fa100](https://github.com/this-is-tobi/rta/commit/45fa100a28300c6603438263538bd42a5c5a2067))
* **fs:** fs hash takes a pasted checksum's first word, so a colon in the filename is not a prefix ([5200e58](https://github.com/this-is-tobi/rta/commit/5200e584291df5a2de3ed777500fe02d867f5135))
* **fs:** fs tree's detail names the walk's bounds by their inputs, never as flags ([b65175f](https://github.com/this-is-tobi/rta/commit/b65175f4e7b9243d17bc27dc7a1f246bc0b08581))
* **gen:** gen overview's Command column is the call an agent makes, not a command line ([8289c09](https://github.com/this-is-tobi/rta/commit/8289c09424ecf295262cd3b23606978a4679eea0))
* **git:** a .git file, commondir or alternates entry leading out of the roots is refused ([5a56e6a](https://github.com/this-is-tobi/rta/commit/5a56e6aba8a0ce84a3402830f3e9265f7f1dd32d))
* **git:** a bare repository under the roots opens over MCP, and its file is named from its root ([9ede3d2](https://github.com/this-is-tobi/rta/commit/9ede3d2cd68dc50359be1319934f484b12f125a1))
* **git:** a diff names a file the path gate refuses instead of showing it, rta's own state first ([b553ce9](https://github.com/this-is-tobi/rta/commit/b553ce988cc818c1e4aff422a17cba4a21e7a26b))
* **git:** a diff names a pipe, socket or device in the working tree instead of waiting on it ([672b764](https://github.com/this-is-tobi/rta/commit/672b76406ab1f8b35292d675b3c458a4e1fe4aac))
* **git:** a diff shows a symlink by its text, and names a file it cannot read beside the rest ([912a800](https://github.com/this-is-tobi/rta/commit/912a800c96280837b9331c46052ee0cc3286d7c3))
* **git:** a pipe where a repository keeps a file is refused, not waited on ([672edb0](https://github.com/this-is-tobi/rta/commit/672edb01df1c98a8a36f8f7c6339b18fc624ad01))
* **git:** a worktree diff lists its files in path order, as git does, the same on every call ([4078e42](https://github.com/this-is-tobi/rta/commit/4078e42bc3f22c13b8eb73753255299cb97ded52))
* **git:** a worktree diff names a moved submodule by its commits, as a commit's diff does ([3746b19](https://github.com/this-is-tobi/rta/commit/3746b195b45b8fd58c686d17ecc09667c3a8936e))
* **git:** a worktree diff reads a bounded amount in all, as a commit's does ([717fdba](https://github.com/this-is-tobi/rta/commit/717fdbadeb5ea95535d28086a532931e7d412736))
* **git:** blame's and log's file is taken from the current directory on every surface, as git does ([237efed](https://github.com/this-is-tobi/rta/commit/237efed418c332bfbf841101165f1c68a8da607e))
* **git:** git blame reads at most 64 MiB of a file's history in all, and refuses one past it ([c1ac417](https://github.com/this-is-tobi/rta/commit/c1ac4172f3ae1acb81551083b25a1fe108fe901c))
* **git:** git blame refuses a file over 16 MiB at HEAD or in its history instead of reading it ([e1605e8](https://github.com/this-is-tobi/rta/commit/e1605e83e0eec7e2fcca2cb06f7452062bc62a2e))
* **git:** git config shows both global files git reads, not the first of them that exists ([85e6e6e](https://github.com/this-is-tobi/rta/commit/85e6e6ec105d522296f2baabcfa3db1c0d8f141b))
* **git:** git hooks lists the directory core.hooksPath names, and where each hook is ([15bcd02](https://github.com/this-is-tobi/rta/commit/15bcd02af1d06444932b924ccea27001cac620fd))
* **git:** git status, diff and overview write nothing for a submodule initialised and never cloned ([7eab404](https://github.com/this-is-tobi/rta/commit/7eab404e4878fdd4722019b7821dce2d6e5aae49))
* **grant:** a call's record is judged as the handler receives it, padding and all ([1940b7f](https://github.com/this-is-tobi/rta/commit/1940b7f9afb841c6ad6eab7d30a6aa21e42866ca))
* **grant:** a folder grant covers no record a target would decode into a climb out of it ([06722cf](https://github.com/this-is-tobi/rta/commit/06722cfaa050b6f4ff55c37e2237a781ff7fd34d))
* **grant:** grant list with the guard orphaned answers its table, the tamper sign as a warning ([c2993fc](https://github.com/this-is-tobi/rta/commit/c2993fcbde9abb8a20245f0499ddda4f33e56b83))
* **grant:** grant names its inputs and next calls as the TUI and the CLI each spell them ([e9c5521](https://github.com/this-is-tobi/rta/commit/e9c5521fb3d401d9866459425f01f671cba81558))
* **grant:** grant revoke and list given a server narrow to --role, as they do locally ([e26e821](https://github.com/this-is-tobi/rta/commit/e26e82118fa7bb0b9bb5f4554484dab51f24b6db))
* **http:** a blocked destination's hint tells a person the guard holds at the terminal too ([0c72fad](https://github.com/this-is-tobi/rta/commit/0c72fadd3d74aec2dfaec00ee67d52eb21d4c2f3))
* **http:** a proxy named without a port is trusted at the port the transport dials it on ([d83c70f](https://github.com/this-is-tobi/rta/commit/d83c70f103c74c444fada4614c18b5e2218ef574))
* **http:** an http refusal names the timeout argument and http.status as the caller has them ([a3cd84f](https://github.com/this-is-tobi/rta/commit/a3cd84f84caf9b422865832fa12307ad8bc19746))
* **http:** http refuses an IPv4-translated or a Teredo address that carries a blocked IPv4 one ([9a875ef](https://github.com/this-is-tobi/rta/commit/9a875ef91b7da7b1bff074db4b5ffc3c2484542c))
* **http:** http refuses shared, reserved and translated addresses, cloud metadata's among them ([70f8afd](https://github.com/this-is-tobi/rta/commit/70f8afdd1ec87c944a3f5b811b2030d741aef7fc))
* **init:** init --dry-run asks its questions and writes nothing, and says what it would write ([5ccb5bd](https://github.com/this-is-tobi/rta/commit/5ccb5bd39f9f179920564ae490d15d4cff86eb79))
* **init:** init answers with the file it wrote and the answers it holds, as -o asks ([a253377](https://github.com/this-is-tobi/rta/commit/a253377c9044e5478a9ca8762e84783dfc04cfb6))
* **init:** init asks its questions on the terminal whatever stdout is, and answers on stdout alone ([502a02b](https://github.com/this-is-tobi/rta/commit/502a02bdfd19fe3ba63ba5216659554f4853d349))
* **keys:** keys names its inputs and next calls as the TUI and the CLI each spell them ([4d5fd8e](https://github.com/this-is-tobi/rta/commit/4d5fd8ee12810ba86b2cce159f7c3827c57c0520))
* **kv:** a locked key on a store that exists points at kv.rekey for one that needs no passphrase ([cfb584d](https://github.com/this-is-tobi/rta/commit/cfb584df66ad6e45ec1f0144ad05f0ba66faf8bb))
* **kv:** a recipient that spells a key is that key, never a file named after it ([99b25bc](https://github.com/this-is-tobi/rta/commit/99b25bc1f17a3a67a10b2bc2a450fa775bdf7872))
* **kv:** a rename grant has to cover the name a key moves to as well as the key ([3f26f4b](https://github.com/this-is-tobi/rta/commit/3f26f4b8b4cfec57614a08004ab5053cc1babae5))
* **kv:** kv env refuses two keys that become one variable rather than printing both ([610bd7e](https://github.com/this-is-tobi/rta/commit/610bd7ea9d042b09f8b409021ac9a2d52aef68e6))
* **kv:** kv names its tools, inputs and calls as the surface asking has them ([c46bd90](https://github.com/this-is-tobi/rta/commit/c46bd90b79fc9dd85d3e469b6b0bc1bb3bfe3f66))
* **lock:** a lock's page names the call that lifts it as the surface showing it ([1a37f9d](https://github.com/this-is-tobi/rta/commit/1a37f9d044c5618341049cfb88400c480671cdbd))
* **mcp:** a handler is told a path it was given was a link, so net resolver list says who owns it ([2ee0d7d](https://github.com/this-is-tobi/rta/commit/2ee0d7d9657a4efc610ca36fe0325ee59eff4b15))
* **mcp:** a required input the operator's config gives is not required in the tool's schema ([c06776a](https://github.com/this-is-tobi/rta/commit/c06776a3cd85b64580751f8a7670e4e3b12ae6d6))
* **mcp:** a tool description names an input as `key` and a capability by its ID, not a flag ([3d1f399](https://github.com/this-is-tobi/rta/commit/3d1f3997fee022eaa2e819e14a66d819bb4d4019))
* **mcp:** an agent reads a command line only as one to ask the operator to run ([58d0a93](https://github.com/this-is-tobi/rta/commit/58d0a9307e9632bda5a57502b40bbd5e15a6572c))
* **mcp:** mcp install answers with what it registered or the block to add, as -o asks ([c9336c3](https://github.com/this-is-tobi/rta/commit/c9336c3267b36311f03789bcd78d05e9017e98d1))
* **mcp:** mcp install prints the block to paste as it is, after its pairs, on any terminal ([b98d224](https://github.com/this-is-tobi/rta/commit/b98d224bfe1023062ad2ac9ae5ab8461650b1afe))
* **net:** a hosts or resolver backup creates rta's data directory owner-only, and is 0600 ([d7fba2f](https://github.com/this-is-tobi/rta/commit/d7fba2fc747e42d429d641b8bf6905f89ab0a36f))
* **net:** a hosts or resolver write refused for want of root names the command for whoever runs it ([7741fdc](https://github.com/this-is-tobi/rta/commit/7741fdce66b684c4c2bfc486d6054e8b1a0721a9))
* **net:** a net refusal names its arguments and tools as the surface asking gives them ([041a201](https://github.com/this-is-tobi/rta/commit/041a2013389bd59fa72ccd72aef97506abca95d8))
* **net:** net hosts add, rm and toggle refuse a hosts file that is a symlink, naming its target ([bf0f6b6](https://github.com/this-is-tobi/rta/commit/bf0f6b6c94664627636a5a0cd40f844dea2e01fd))
* **net:** net hosts toggle parks a name live on any line, and will not guess between parked ones ([066fc67](https://github.com/this-is-tobi/rta/commit/066fc67f734e39134e692b3f1b2bd34574fbc50c))
* **net:** net info masks a proxy's userinfo up to its last @, where net/http ends it ([7f47130](https://github.com/this-is-tobi/rta/commit/7f47130a8fc87507aef6dd154724b7fd919add3e))
* **net:** net listen lists Linux's UDP sockets, whose missing peer reads as 0.0.0.0 port 0 ([90a3582](https://github.com/this-is-tobi/rta/commit/90a35828e5153f3ac908df4062211248d0d922d1))
* **note:** a note cannot go under its own sub-note, and a cycle already made stays listed ([fbbf4a6](https://github.com/this-is-tobi/rta/commit/fbbf4a61e2fa7cefed69f78160ea21603efaea64))
* **note:** a note left as its own parent is not counted among its own sub-notes ([084b218](https://github.com/this-is-tobi/rta/commit/084b2186faeb74c8e83b97270d8997d0e7aabcd0))
* **note:** a note's hints name note.list and note.toggle as the surface asking calls them ([3f5964f](https://github.com/this-is-tobi/rta/commit/3f5964f3a5ba042806dfe54a02c13349878ea1b3))
* **note:** note rm counts the sub-notes it moved up in the singular when there is one ([75353d3](https://github.com/this-is-tobi/rta/commit/75353d3732223172aaaf721588a1ed6d114160d9))
* **operator:** operator status and init name their next calls as the surface asking has them ([15c0df3](https://github.com/this-is-tobi/rta/commit/15c0df34fa626b16ebaeebf08b801ab2954de8ae))
* **pkg:** a manager whose list exits non-zero is a failed row with its reason, not ok ([cc5234c](https://github.com/this-is-tobi/rta/commit/cc5234cb619d8348e2afab330ff1ad8a6785c2f4))
* **pkg:** a tool's latest release is the version in its tag, so bun-v1.1.38 is newer than 1.1.20 ([d409816](https://github.com/this-is-tobi/rta/commit/d409816fb61b2ff32019873521280c7e45c80712))
* **pkg:** a tool's upgrade takes a .tar.gz or a bare binary, and places only a program it can run ([dae5a96](https://github.com/this-is-tobi/rta/commit/dae5a9685f57e6fb82ff9aa5474575efafdb1a18))
* **pkg:** an installed pre-release is behind the release it leads up to ([c5f76b9](https://github.com/this-is-tobi/rta/commit/c5f76b9245921201edf04e67825fe5d8d1dd9bd4))
* **pkg:** npm's own failure is a failed npm row, not a package called error to install ([2669519](https://github.com/this-is-tobi/rta/commit/266951907c4de79b9741d4ea9c036cd3881c7824))
* **pkg:** pkg names its inputs and capabilities as the TUI and the CLI each spell them ([f3721e4](https://github.com/this-is-tobi/rta/commit/f3721e4e4e01d0e6b8cb3c55664dfca3474bfdd0))
* **pkg:** pkg outdated asks go only about a regular file in GOBIN, not a pipe it would wait on ([99986cd](https://github.com/this-is-tobi/rta/commit/99986cd90bc8948482eab94fc14ab4758a4eb222))
* **pkg:** pkg outdated names a broken pipx venv on its own row and grades the healthy ones ([e61ebb4](https://github.com/this-is-tobi/rta/commit/e61ebb4583ebd2da1a7d65100782c6b984de83d4))
* **plugin:** a credential grant opens only what the plugin declared, and never from the image ([73a2609](https://github.com/this-is-tobi/rta/commit/73a260923ff9cbd592858f456736c8b291caf1ce))
* **plugin:** a list argument is the last the command line takes, or the plugin is refused ([614e54c](https://github.com/this-is-tobi/rta/commit/614e54c0b20b2cfa3e7b128c8bc06bf217f02c70))
* **plugin:** a required input with a default is refused, as the default leaves it never missing ([706aaba](https://github.com/this-is-tobi/rta/commit/706aabaebc769cbe0ca28eb9cc927918cce593d8))
* **plugin:** an untrust that leaves the system root trusting a plugin says it keeps loading ([130d21e](https://github.com/this-is-tobi/rta/commit/130d21e7025da49d8a6667d00e39e2b67c81a500))
* **plugin:** install and upgrade read no manifest through a symlinked index/ directory ([e7f447c](https://github.com/this-is-tobi/rta/commit/e7f447c7ec4b799796e98e68dd9012e0948edd23))
* **plugin:** plugin index update refuses a pull that leaves the index no manifest to read ([6950f7c](https://github.com/this-is-tobi/rta/commit/6950f7cac7fe6715b40588bd0172acf067970a9e))
* **plugin:** plugin new answers with the directory it wrote and what to run next, as -o asks ([9fdd5c2](https://github.com/this-is-tobi/rta/commit/9fdd5c2907f71cf22aaba2177994694ab8486476))
* **plugin:** plugin untrust answers with what it withdrew and the record, in the format asked for ([102dc56](https://github.com/this-is-tobi/rta/commit/102dc56c6e0c9ed592f2909d851098aca537bef9))
* **plugin:** remove and prune delete a stored copy whose digest the system root trusts too ([6af0ff0](https://github.com/this-is-tobi/rta/commit/6af0ff026779395ad8cc78d5b0fc406f8e36b3a7))
* **plugin:** remove names what the system root still trusts rather than saying all trust went ([251c6d1](https://github.com/this-is-tobi/rta/commit/251c6d11e411e865538a4c39a0c7e03ba7301d95))
* **policy:** policy init answers with the file it wrote and its ceiling, in the format asked for ([d371342](https://github.com/this-is-tobi/rta/commit/d371342655c9ad1b45709499cb85ed4332df1da4))
* **policy:** policy require answers with the file it wrote and whether this directory meets it ([37b5c60](https://github.com/this-is-tobi/rta/commit/37b5c60af82eac8ca90ac6718d8c36349cb60070))
* **policy:** policy show's repository policy row names the repository's files alone ([7ec7566](https://github.com/this-is-tobi/rta/commit/7ec75667526bb417c344be7372fa573cd1b70c4e))
* **sys:** sys ps names its sort input as the surface asking gives it ([58bdd69](https://github.com/this-is-tobi/rta/commit/58bdd6925e726ded8e66dbd6bfecd0b45d120508))
* **sys:** sys ps on macOS counts another user's process as unreadable rather than idle and empty ([24b1a7a](https://github.com/this-is-tobi/rta/commit/24b1a7a99b3690b41161a35dd71be3c849cbcca0))
* **sys:** sys ps ranks processes by the CPU they used over the last 200ms, not since they started ([32fe3f1](https://github.com/this-is-tobi/rta/commit/32fe3f1cb5fdde57b73ac97a149202f5a6ffd0c9))
* **tui:** a required input with an empty default keeps a capability off the dashboard ([da16bda](https://github.com/this-is-tobi/rta/commit/da16bdae50a2869848870258290d30bd52b40d49))
* **tui:** the TUI names a capability it sends somebody to as its catalogue lists it ([2ececa1](https://github.com/this-is-tobi/rta/commit/2ececa1b5a9fa5305a596e1f9b6b27273eef4a24))


### Code Refactoring

* **plugin:** the CLI's spelling of a capability as a command line is written once ([7f6e250](https://github.com/this-is-tobi/rta/commit/7f6e250cc33f007ce8192ac66efc0aa558b70532))

## [0.27.0](https://github.com/this-is-tobi/rta/compare/v0.26.0...v0.27.0) (2026-09-26)


### ⚠ BREAKING CHANGES

* **plugin:** a required argument left out over MCP is refused as core.input.missing, not core.mcp.badargs. A required input given as empty text or an empty list is refused on every surface instead of reaching the handler, and sdktest no longer drives a capability whose WithInputs gives one that way. A required Local input nothing supplied is refused over MCP as core.input.missing before the handler runs.
* **grant:** with the guard orphaned, the "granted" section of `grant list --detail` is {"type": "table", "rows": []} to -o json and -o yaml where it was {"type": "text", "body": "guard  ORPHANED — ..."}; the "guard" section before it still carries that line.
* **git:** git diff --commit on a root commit answers its patch where it answered a sentence, and on an empty commit answers {"type": "text", "body": ""} to -o json, -o yaml and MCP where the body was the sentence; pretty output into a pipe or a file writes nothing for it.
* **audit:** audit clients --fix on a machine with nothing to fix answers -o json and -o yaml with {"type": "sections", "items": []} where it answered {"type": "text", "body": "nothing to paste — ..."}, and pretty output into a pipe or a file writes nothing. A script that read `.body` should test `.items | length == 0` instead.
* **plugin:** `rta plugin trust` with no argument and nothing waiting answers -o json, -o yaml and -o csv with the table and no rows, where it answered {"type": "keyvalue"} with "waiting" and "trusted" pairs. A script that read those pairs should test `.rows | length == 0`; `rta doctor` reports how many artifacts are trusted.
* **net:** `rta net probe` and `rta net send` against a port that sends nothing answer the "response" section with {"type": "text", "body": ""} in -o json, -o yaml and MCP, where the body explained the silence. Test the connection section's "received" or an empty body instead.
* **kv:** `rta kv env` on an empty store writes nothing to a pipe or a file, and answers -o json, -o yaml and MCP with {"type": "text", "body": ""} where the body was "# no keys stored". Test for an empty body instead.
* **note:** `rta note show` of a note with no body answers the "content" section with {"type": "text", "body": ""} in -o json, -o yaml and MCP, where the body was a sentence saying the note is empty. Test for an empty body instead.
* **kv:** `rta kv recipients` on a store with no key recipients, or with no store at all, answers -o json, -o yaml and MCP with {"type": "table", "rows": []} where it answered {"type": "text", "body": ...}. A script that read `.body` should test `.rows | length == 0` instead, and `rta kv status` says what the store is locked with.
* **grant:** an empty `rta grant list`, with or without --server or --role, answers -o json and -o yaml with {"type": "table", "rows": []} where it answered {"type": "text", "body": ...}, and with a "sections" view holding that table and a "policy" section when the team's policy suppresses grants. A script that read `.body` should test `.rows | length == 0` instead.
* **fs:** `rta fs usage --depth -1`, a `depth: -1` config value or an MCP call passing a negative depth is refused with core.input.range where it scanned without a limit. Pass 0, which is what -1 meant.
* **kv:** `rta kv tree` on an empty store answers -o json, -o yaml and MCP with {"type": "tree", "roots": []} where it answered {"type": "text", "body": "No keys stored yet ..."}. A script that read `.body` should test `.roots | length == 0` instead.
* **git:** `rta git diff` on a clean working tree answers -o json, -o yaml and MCP with {"type": "text", "body": ""} where the body was "no uncommitted changes", and pretty output to a pipe or file is empty. A script that compared the body to that sentence should test for an empty body instead.
* **policy:** policy init over an existing .rta-policy.yaml or unable to write it, and policy require when it cannot find, read or write the operator's policy file, exit 1 where they exited 2. A script that branched on exit 2 should branch on 1, or on the code.
* **init:** rta init run with no terminal, or left before it writes, exits 1 where it exited 2, and says so as core.init.terminal or core.init.aborted. A script that branched on exit 2 should branch on 1, or on the code.
* **mcp:** mcp serve exits 1 where it exited 2 when a listener cannot bind, the token file, roster or root cannot be read, the OIDC issuer does not answer, the guard is bound to another URL, or the transport fails. A supervisor that branched on exit 2 for these should branch on 1, or on the error's code.
* **install:** codec b64, hex and url --decode -o json answer decoded text holding a NUL, a control or an invisible character as sections (its value item beside a bytes item holding the hex dump), and bytes that are not UTF-8 as a text view whose body is the dump, where 0.26.0 answered a text view whose body held the bytes. A script reading .body gets null for the first; the exact value is the value item's body.
* **cli:** -o yaml, -o csv and -o md write the nine characters that reorder text (U+202A to U+202E, U+2066 to U+2069) as a backslash-u spelling, where 0.26.0 wrote the characters themselves. -o json keeps them exact: read from json any value a script compares.
* those listings, when empty, answer -o json, -o yaml and MCP with {"type": "table", "rows": []} where they answered {"type": "text", "body": "..."}. A script or agent that read `.body` to learn the listing was empty should test `.rows | length == 0` instead; -o csv prints the header row and exits 0 where it exited 2.
* **mcp:** over MCP, the operator's config and a profile's set: now apply to inputs that declare a default, as they do on the CLI. An agent's call that ran with the default now runs with the configured value, and one the CLI refuses for its configured value (a value outside the options, a path outside the server's root) is refused over MCP too. To keep agents on the default, remove the key from the config or have the agent pass the value.
* **mcp:** over MCP, a value outside a field's options is refused as core.input.option and a number outside its range as core.input.range (or core.input.type when the field has no range), not core.mcp.badargs. An integration matching core.mcp.badargs for those mistakes should match the core.input.* codes; a wrong type or an unknown argument is still core.mcp.badargs.
* **plugin:** a plugin declaring a fractional Min or Max on an Int input now fails to load. Write the bound as a whole number, or declare the input a Float.
* **sdktest:** sdktest's conformance run holds the values a test sends through WithInputs to the capability's options and its Min and Max, as the host does, so a plugin whose test sends a value outside them fails on the SDK bump alone; send a value the declaration takes.
* **profiles:** a value in the config's plugins: block outside an input's options (plugins.gen.encoding: b64), which 0.26.0 passed to the handler, is refused on every call that reads it, on every surface; `rta doctor` names each one. A listed value in another case is written as declared on the CLI and in the TUI; over MCP the published enum is matched exactly, so it is refused there.
* **plugin:** a fractional value for an integer input in plugins:, a profile or a tile's with: now refuses every call reading it instead of running truncated. Write a whole number.
* **plugin:** a value in plugins: or in a tile's with: whose YAML type differs from the declared one, such as `tls: true` on a text input or `tls: "true"` or `tls: yes` on a boolean, now refuses every call reading it with core.input.type instead of running with an empty string or false. Write it as the refusal and `rta doctor` say: quote text, and write a boolean unquoted.
* **plugin:** a number written as text in the config's plugins: block (limit: "3"), which 0.26.0 did not refuse, is refused on every call that reads it. Write it bare (limit: 3); `rta doctor` names each one.
* **plugin:** a plugin declaring Options on an Int, Float, Bool, Text, Path, Secret or SecretSlice input now fails to load. Bound a number with Min and Max, drop Options from a Bool, or declare the input a String.
* **dashboard:** dashboard add --set no longer splits on commas, so --set host=a,port=1 is one value for host rather than two inputs. Give each input its own --set.
* **cert:** cert chain, cert inspect and cert pem refuse a file holding a certificate block that is not valid PEM, where they used to leave it out and exit 0, and a damaged last block is cert.parse.failed rather than cert.file.truncated. Repair or re-export the named certificate, or remove it from the bundle.
* **gen:** gen password and gen uuid refuse --count 0 or a negative count with core.input.range, where they used to generate one value; pass --count 1, or leave --count off. A count over 1000 is refused as core.input.range rather than gen.count.toomany, so a script matching the old code should match core.input.range.
* **plugin:** a declaration whose Default falls outside its own Options or its Min and Max fails validation, so a plugin carrying one no longer loads.
* **plugin:** an Int or Float input outside its field's Min and Max is refused as core.input.range, where the host used to clamp it into the range and run.
* **plugin:** a value outside a field's Options is refused as core.input.option on every surface, a plugin's capabilities included, where it used to reach the handler; a value naming an option in another case is rewritten to the declared spelling.

### Features

* **cli:** pretty output takes COLUMNS when it is set, a pipe included ([2102ed5](https://github.com/this-is-tobi/rta/commit/2102ed5b10a53b027d16e6e1dbc4665bff91ffde))
* **codec:** codec.jwt verifies a signature against a key or secret the person gives ([8aeb37e](https://github.com/this-is-tobi/rta/commit/8aeb37ed88cb09363ecb1f63cd201a60e9564edc))
* **codec:** every JOSE token shape decodes, and codec.jwk reads keys and key sets ([31318e4](https://github.com/this-is-tobi/rta/commit/31318e47ded0b013b751f80f4c52790d1c0bbde1))
* **debug:** debug.ansi names the characters that hide themselves, and decodes tag text ([67ac437](https://github.com/this-is-tobi/rta/commit/67ac437efbbd9328230159dfb83b42a934d84cac))
* **format:** a plugin can show bytes that may not be text, the way built-ins do ([7501f19](https://github.com/this-is-tobi/rta/commit/7501f1908ce7a468e25537b3d70c9777f692f492))
* **plugin:** an empty result's sentence crosses the plugin wire ([a1ebd6b](https://github.com/this-is-tobi/rta/commit/a1ebd6b1317f43ee8057d82c343c0dba41608663))
* **plugin:** an input the CLI reads from a pipe is declared Piped, and only a built-in may be ([804c1f2](https://github.com/this-is-tobi/rta/commit/804c1f242b024463b0eb8e50fdccaf697b08a565))
* **view:** an empty page of sections carries the sentence a person is shown in its place ([d95cd8c](https://github.com/this-is-tobi/rta/commit/d95cd8cabdafb2f0d7bdc7534ed20067be95399b))
* **view:** an empty table carries the sentence a screen shows in its place ([dcaf8d3](https://github.com/this-is-tobi/rta/commit/dcaf8d30aa1eb16343b107cba390b02ece137cd0))
* **view:** an empty text or tree carries the sentence a person is shown in its place ([c2e8a9b](https://github.com/this-is-tobi/rta/commit/c2e8a9b396acc8f9b5a12fc47d285512dbd35e2e))


### Bug Fixes

* **agent:** an empty queue read with --server offers the command that answers that server ([210debb](https://github.com/this-is-tobi/rta/commit/210debbc7a5af0b340722f62a8ee6b029a5cc11b))
* an empty listing answers with its table, and says it is empty only on a screen ([e306f1d](https://github.com/this-is-tobi/rta/commit/e306f1dad51d9df96f6223bc3189ad7038917075))
* **audit:** a detail pass cut short by its deadline says so, however the ids went out ([bd760cf](https://github.com/this-is-tobi/rta/commit/bd760cf564d262b22eae4ca7bc4c07554e687e1d))
* **audit:** an advisory with no fix names the packages it is about ([b088478](https://github.com/this-is-tobi/rta/commit/b088478b408a512f80ffc038c2626fee9741ed38))
* **audit:** audit clients --fix with nothing to fix answers an empty page, and says so on a screen ([76ab207](https://github.com/this-is-tobi/rta/commit/76ab20703988db5290106b15a1c802ead98e272e))
* **cert:** a certificate that does not parse is placed in its file ([332f8c7](https://github.com/this-is-tobi/rta/commit/332f8c7f6a8b42fcea3287bbe28e032b72212ea1))
* **cert:** a damaged certificate block is refused wherever it sits in the file ([ca73fc3](https://github.com/this-is-tobi/rta/commit/ca73fc369614c583b4fa3fd191ce0555a97989f4))
* **cli:** -o csv marks every line of a note with "#", not only the first ([e51d634](https://github.com/this-is-tobi/rta/commit/e51d6340438227f3ef036adc22c4a8ee00ca4f10))
* **cli:** -o csv of an empty result is its header row alone, whatever its shape ([91e8edd](https://github.com/this-is-tobi/rta/commit/91e8edd308346370b7e31da55431c8d9b9dcdf8a))
* **cli:** -o csv reports what a table could not read, beside its row count ([959e8ab](https://github.com/this-is-tobi/rta/commit/959e8ab35b6bdf1b926521eef960ba92645977b1))
* **cli:** -o csv writes every result, so a write that landed never exits 2 ([3c9dc01](https://github.com/this-is-tobi/rta/commit/3c9dc0170ca8f3eae7d90aaf73d52f1aaa6eaa71))
* **cli:** -o md draws an empty result's sentence as its own lines, block syntax escaped ([ba39bef](https://github.com/this-is-tobi/rta/commit/ba39bef29d6813cbf1c669c5767f5f75243a6111))
* **cli:** -o md escapes a backslash, so one in the data cannot undo the escape after it ([8dc7471](https://github.com/this-is-tobi/rta/commit/8dc74717e39abbf20318f78b2e243cd233c1da83))
* **cli,tui,grant:** "n of total" counts agree with the total, so one row is one row ([79f9b8a](https://github.com/this-is-tobi/rta/commit/79f9b8ae1a4361482f98c4b452156428bce17977))
* **cli:** a default output format nothing renders is refused as core.output.invalid, before a write ([f87db34](https://github.com/this-is-tobi/rta/commit/f87db34e61f4bcf5be31b36e1a8bc7ca9d9f349d))
* **cli:** a non-breaking hyphen in the data is drawn as itself, not as "-" ([625b8df](https://github.com/this-is-tobi/rta/commit/625b8df3cf5aa6338b52990288236787be483fc8))
* **cli:** a tab in an error survives -o yaml, as it does in a result ([d3c99f7](https://github.com/this-is-tobi/rta/commit/d3c99f7eb590bbbfc04675123b8a6a80c82d7b19))
* **cli:** a table drawn as records says it continues and what it could not read ([31140d8](https://github.com/this-is-tobi/rta/commit/31140d8886ff9d51b5d0a6525ef06fbf8218d298))
* **cli:** a table just wider than the terminal is shrunk, not cut at the edge ([6f275a8](https://github.com/this-is-tobi/rta/commit/6f275a84867103a06b3b13622b8fd613cb4db1c7))
* **cli:** a value laid out in lines moves under its key rather than break ([8263bea](https://github.com/this-is-tobi/rta/commit/8263bea3ac729eba52f23f28993a6cddc54be681))
* **cli:** a wrong command line is refused as core.usage, in the format asked for ([0b5fbfc](https://github.com/this-is-tobi/rta/commit/0b5fbfcf6b211d082425588f2b9beff1293a2c10))
* **cli:** an empty listing's sentence is drawn on a screen and in md, never into a pipe ([288eb87](https://github.com/this-is-tobi/rta/commit/288eb87ae57909bef158fdc3e74d3c487aa5ee94))
* **cli:** an exported RTA_OUTPUT holds over a config file that does not parse ([db4223f](https://github.com/this-is-tobi/rta/commit/db4223f74f2614962011db398fc1683f6c914c81))
* **cli:** cobra's completion command refuses a wrong argument as core.usage, like every group ([20e49d1](https://github.com/this-is-tobi/rta/commit/20e49d1df2a20855e9855f772b69e74c2d68cf64))
* **cli:** every command renders with options built in one place, Screen and csv notes included ([f121e0f](https://github.com/this-is-tobi/rta/commit/f121e0ff1bea074ffce418b2bbeafae347fc5455))
* **cli:** every shell completion drops a deceptive value and cleans its description ([875ee33](https://github.com/this-is-tobi/rta/commit/875ee33625c73529e2e58a3074df391ee0a915de))
* **cli:** rendered markdown keeps its colour and honest links, and nothing else ([05eee4e](https://github.com/this-is-tobi/rta/commit/05eee4eed80b13a4af3a727171d3fe7e73a96e7e))
* **cli:** shell completion offers no value that draws as something else, and cleans a description ([7a75ebc](https://github.com/this-is-tobi/rta/commit/7a75ebcc368ece241f134e44790eb396c2a21a4b))
* **codec:** a check costs a bounded amount, and stops when its caller has gone ([fc76601](https://github.com/this-is-tobi/rta/commit/fc76601140c5a652ac53ad621725e3de24cff658))
* **codec:** a decode keeps its exact bytes, and hex and base64 read only what they say ([a721e9c](https://github.com/this-is-tobi/rta/commit/a721e9cf5511adb6c7f85667f84d95dad546cd7d))
* **codec:** a JSON JWS whose signatures disagree about b64 is refused, not read one way ([b3d8b32](https://github.com/this-is-tobi/rta/commit/b3d8b32b984d7fc1eee16c7c097b8e20f567aa8d))
* **codec:** a JWE with alg dir is said to use the shared key itself, not to agree one ([be0b020](https://github.com/this-is-tobi/rta/commit/be0b020e11b7ed53a7a8a7daabec129f111e8c20))
* **codec:** a key crypto/rsa refuses is refused as the key, not reported as a tampered token ([360e4da](https://github.com/this-is-tobi/rta/commit/360e4da8befd1393e865afc08df14d2af4b9f352))
* **codec:** a key no verifier accepts is hinted as the problem, not sent to look at its size ([446785a](https://github.com/this-is-tobi/rta/commit/446785a5c77c82f426ba0bb78ed83acd260b17fb))
* **codec:** a key set in the secret file is refused, saying the file takes the one oct key ([5138e91](https://github.com/this-is-tobi/rta/commit/5138e91408a0aab0b971ab05fd9d1502d9417094))
* **codec:** a key skipped for what it declares is hinted at its declaration, not its size ([837b3fb](https://github.com/this-is-tobi/rta/commit/837b3fb9c3e2199e0554e14c6965697408ce801a))
* **codec:** a key's page and a verdict say which key, and why it could not be used ([9b5e1a1](https://github.com/this-is-tobi/rta/commit/9b5e1a182ebb944fb13a3f19594f1f221f465ec7))
* **codec:** a one-line PEM whose footer comes before its body is read, not a crash ([c5f281e](https://github.com/this-is-tobi/rta/commit/c5f281e34f1728be1f1ee6125864ee7495e92384))
* **codec:** a PEM key handed to codec.jwk is sent to codec.jwt, and a certificate to cert inspect ([b381d21](https://github.com/this-is-tobi/rta/commit/b381d210c3e6ab22a85c8204d533268c613c4038))
* **codec:** a private key is measured before it is parsed, and one call reads at most 32 ([1045e08](https://github.com/this-is-tobi/rta/commit/1045e081fb045f0c29003efbda1e6f1a1b2b376e))
* **codec:** a public key saved with a byte-order mark or in UTF-16 is never a secret ([d65cd06](https://github.com/this-is-tobi/rta/commit/d65cd061e1da7207ee764771262c235b9524390d))
* **codec:** a refusal names codec.jwt's key as the surface asking shows it, not as a flag ([b38e1d2](https://github.com/this-is-tobi/rta/commit/b38e1d205eb3f08fb41e0d38399b993298a315d6))
* **codec:** a refused key or token is told what to change, on every surface ([9b66237](https://github.com/this-is-tobi/rta/commit/9b662372853f081fa63c386df6232a24741a4703))
* **codec:** a secret file holding only whitespace is refused as holding no secret ([0320b1b](https://github.com/this-is-tobi/rta/commit/0320b1b8e484cb5f5f40e735068f6cc265d057c0))
* **codec:** a secret saved with a byte-order mark or in UTF-16 is also read as its text ([ac3cf5c](https://github.com/this-is-tobi/rta/commit/ac3cf5c62f2dc2b2191c03735145230aae131fcd))
* **codec:** a shared secret reaches codec.jwt only from a file, and no key stands in ([13efda8](https://github.com/this-is-tobi/rta/commit/13efda89c02628427c4dc2c2e56b4f6713773259))
* **codec:** a usable key is used whatever sits beside it, a signing key included ([0f28cc5](https://github.com/this-is-tobi/rta/commit/0f28cc5c57badf4c6a1d9dc3207a366ddf713c9e))
* **codec:** an oct key in the secret file is held to its use, alg and key_ops ([e7f32f4](https://github.com/this-is-tobi/rta/commit/e7f32f4cd150a3aadea81de281d4f1ce9cd4ec6a))
* **codec:** an RSA key under 2048 bits is named beside its verdict from PEM, as from a JWK ([d658ec7](https://github.com/this-is-tobi/rta/commit/d658ec73035d17f0d28d34e876d9300318977899))
* **codec:** codec.jwt names what a strict JOSE parser would refuse or read otherwise ([9daa65b](https://github.com/this-is-tobi/rta/commit/9daa65b6d0595a3575e1eeb4b254bac3158c9b4a))
* **codec:** codec.jwt's description offers a secret file only as the terminal's ([7880527](https://github.com/this-is-tobi/rta/commit/7880527f51fa72965916a330664060549fe4f20a))
* **codec:** decoded bytes are shown, hex and base64 come in as they are copied ([52962cd](https://github.com/this-is-tobi/rta/commit/52962cd851dd29220b83eaed5d78927847d67ae6))
* **codec:** every reading of the secret file is checked for a public key ([d0df2b5](https://github.com/this-is-tobi/rta/commit/d0df2b5f50739c280b15de2c596ea3d6ca29ec58))
* **codec:** reading nested JSON costs what the input does, whatever its names ([3e95a4c](https://github.com/this-is-tobi/rta/commit/3e95a4ccbef09b3cb2fe188a046153f2c1afc312))
* **codec:** the package doc and the description say what codec.jwt reads and checks ([3dea1af](https://github.com/this-is-tobi/rta/commit/3dea1af25248cb9fd0e889e3d06eb22359e42e7e))
* **codec:** the pure transforms say they are idempotent ([22d2af8](https://github.com/this-is-tobi/rta/commit/22d2af86ce8718acdcc72b08f355bac6d78bf95f))
* **dashboard:** a credential a tile cannot hold is refused without a hint to set one ([13542af](https://github.com/this-is-tobi/rta/commit/13542aff5c7cb39c8414b59a7ca3a0f2a6d07473))
* **dashboard:** a tile and a TUI form ask for an input only the CLI reads from a pipe ([7a96087](https://github.com/this-is-tobi/rta/commit/7a96087077189dc4863cfca7d6b116ac3845939f))
* **dashboard:** a tile is refused a credential, and an option is written as declared ([898368b](https://github.com/this-is-tobi/rta/commit/898368b2bea1e1ff40ff220fc9ebe28aa14aa9eb))
* **dashboard:** add refuses a --set every run of the tile would refuse ([eeeb389](https://github.com/this-is-tobi/rta/commit/eeeb3892fd2c7d1aa77969814f091868aef0c1f5))
* **dashboard:** dashboard add --set takes a comma as part of the value ([a5668d2](https://github.com/this-is-tobi/rta/commit/a5668d2b2f83d8aee3a49c25dc297abe2c997ee5))
* **dashboard:** the way back dashboard rm prints pastes as the tile it removed ([e139aa9](https://github.com/this-is-tobi/rta/commit/e139aa9b54b84b89586aa67d98e57dbfee9affb4))
* **debug:** a pipe is read up to a bound, through one stdin reader shared with keys ([496a65e](https://github.com/this-is-tobi/rta/commit/496a65eb379a5f9f9d737ee8a7ddea13d34623bd))
* **debug:** a raw 8-bit C1 byte is named as its UTF-8 form is ([2c6adce](https://github.com/this-is-tobi/rta/commit/2c6adce8752970facc9da06fccdb280d23766f74))
* **debug:** a sequence the input ends inside, or cut short, is explained as incomplete ([19751c4](https://github.com/this-is-tobi/rta/commit/19751c433c803e6784e497a96b354fffe81db656))
* **debug:** a sequence with more than 32 parameters is explained, not a crash ([c64873e](https://github.com/this-is-tobi/rta/commit/c64873ec8914bcbdf5df2419a2d675197eb0ba3a))
* **debug:** a title or link target is shown escaped, like the sequence carrying it ([541d705](https://github.com/this-is-tobi/rta/commit/541d705355f3b93cc190feac776d8c00c76fc73a))
* **debug:** an 8-bit sequence is explained as one, and a run of variation selectors decoded ([82e09a3](https://github.com/this-is-tobi/rta/commit/82e09a37f09190e9283a8634e7a7a28662bcea4d))
* **debug:** an osc is named by the number it carries, and one with none says so ([c3de614](https://github.com/this-is-tobi/rta/commit/c3de61408b9755744779e351dcd119e5589c9e07))
* **debug:** debug.ansi's empty-input hint names the argument off the CLI, not a pipe ([f576128](https://github.com/this-is-tobi/rta/commit/f5761284246f6895afe83d0a7b5521aa92f2fb16))
* **doctor:** a profile naming an installed, unapproved plugin says to trust it, as profile list does ([fcbae34](https://github.com/this-is-tobi/rta/commit/fcbae341a2209a2adfa420e72ee07f1ca430a0d2))
* **explain:** the card names an input's range beside it ([e5b2a83](https://github.com/this-is-tobi/rta/commit/e5b2a83abcadd0a37dc5b0061b79924d3ee28930))
* **format:** an instant centuries away is counted in years, and a named year 1 is not "never" ([0482608](https://github.com/this-is-tobi/rta/commit/0482608d6c419a8cdf1604e46c162d8f1e48d84a))
* **format:** plain text is told from binary, and the rest is left to the renderer ([925afbc](https://github.com/this-is-tobi/rta/commit/925afbc7c2ce27c6f26a2744e9c0d01ec94b8b40))
* **fs:** fs usage of an empty directory says it is empty on a screen ([69c5536](https://github.com/this-is-tobi/rta/commit/69c5536f8fd86d63b5a5f28c50cd39724bcc0abe))
* **fs:** fs usage takes a depth of 0 or more, and refuses a negative one ([f34dccf](https://github.com/this-is-tobi/rta/commit/f34dccf508feee0ff68075625e573acb99764135))
* **gen:** a count outside 1 to 1000 is refused, not turned into one value ([8a3a5f8](https://github.com/this-is-tobi/rta/commit/8a3a5f83b7e8b7c1f4b15405d70f1822d8e7f9ef))
* **gen:** a length of zero is refused, not turned into one character ([14b6e72](https://github.com/this-is-tobi/rta/commit/14b6e721f5b572dcc4cd56e93098f90ada1a2f1f))
* **git:** a commit's diff reads a bounded amount, per file and in all, as the worktree's does ([d2c162e](https://github.com/this-is-tobi/rta/commit/d2c162e1d0ba25ac147165022aa6aeb9c95df745))
* **git:** git diff --commit shows a root commit's files added, and an empty commit as no patch ([c3a2c4c](https://github.com/this-is-tobi/rta/commit/c3a2c4c7b32dc52c69e8279678ddf0f9b6061b5f))
* **git:** git diff on a clean tree is an empty patch, and says so on a screen alone ([9767eb9](https://github.com/this-is-tobi/rta/commit/9767eb9fa3ecfb1f05afe147e48a0cda6fc5842b))
* **grant:** an empty grant list answers with its table, here and with --server ([d96075a](https://github.com/this-is-tobi/rta/commit/d96075a8190f724e93cd3b047ed4b1ce88131654))
* **grant:** grant list --detail states the guard once, in the section that leads the page ([d8c0f61](https://github.com/this-is-tobi/rta/commit/d8c0f6174c0fa6068ea9ae316d8bd42cf0b67e97))
* **grant:** grant list --role that matches no grant says so, not that no grant stands ([38081ab](https://github.com/this-is-tobi/rta/commit/38081ab432ea6fadb510c884452fb04c5656b76e))
* **grant:** the roster says when a server on another build will decide it ([1cfc289](https://github.com/this-is-tobi/rta/commit/1cfc289451e2aa3e3fef2c90681eb6a9ab01c1e4))
* **http:** a HEAD response's size is the length it declares, not the body it cannot have ([1b59c21](https://github.com/this-is-tobi/rta/commit/1b59c21c2de5fb3aa930332e0d2793789e484a38))
* **http:** a JSON body is indented only when its layout stays within four times its size ([059a5c5](https://github.com/this-is-tobi/rta/commit/059a5c52abb99b375acfac1d4b453e1576fee32b))
* **http:** a JSON body is shown as the server sent it, and a binary one is dumped ([01141f4](https://github.com/this-is-tobi/rta/commit/01141f44c359dd1b03faa2b64e4b81d7e7135259))
* **http:** a JSON body that is not UTF-8 is dumped, as any other body that is not text is ([e930b02](https://github.com/this-is-tobi/rta/commit/e930b028d7d8573a61b4ca4d077b6484475d28fb))
* **http:** http.head's summary names what it shows, not the headers it leaves out ([85deffc](https://github.com/this-is-tobi/rta/commit/85deffc5d71d63f45a217a47ee7cd238dfcc92f4))
* **http:** the size is the response's, and how much of the body is shown is said beside it ([236c9c4](https://github.com/this-is-tobi/rta/commit/236c9c418971d9e14ed2aff315ecb291b45da18f))
* **init:** init with no terminal, or a wizard left, is a coded refusal in the format asked for ([16c8eea](https://github.com/this-is-tobi/rta/commit/16c8eea193f3cdd753870494d5c57fd724626824))
* **init:** the wizard offers no tile an input read from a pipe would leave empty ([dd70f91](https://github.com/this-is-tobi/rta/commit/dd70f910725cdc56151581dd5ef8732dfb8b33bf))
* **kv:** kv env of an empty store exports nothing, and says so on a screen ([508cbe2](https://github.com/this-is-tobi/rta/commit/508cbe26891e1edff8a989d9f795825ae6e39acf))
* **kv:** kv recipients with no key to list answers with its table ([31141dc](https://github.com/this-is-tobi/rta/commit/31141dcd03beea215331f73fc11cbcda108de4ce))
* **kv:** kv tree of an empty store is a tree with no roots, and says so on a screen ([a68efd2](https://github.com/this-is-tobi/rta/commit/a68efd2a466628922eadc5a5fbcd2526409743c7))
* **mcp:** a config value the input guard refuses gives the grant use back, and reads as refused ([ecd61f0](https://github.com/this-is-tobi/rta/commit/ecd61f0164691248ddb020f1a73968b04980f153))
* **mcp:** an argument outside its options or range gets the CLI's refusal, before the gate ([c439030](https://github.com/this-is-tobi/rta/commit/c4390309001d19ff5d43baa8004d4a99d0630c58))
* **mcp:** an input only the CLI reads from a pipe is required over MCP, as its description says ([cd258f4](https://github.com/this-is-tobi/rta/commit/cd258f45be8ab4dcca3851c3ade7bc218623ba16))
* **mcp:** config and a profile's set: apply to an agent's call, and the gate sees what runs ([72aeddc](https://github.com/this-is-tobi/rta/commit/72aeddc5c103e8b4dd83a2649b2a42b1762e25ac))
* **mcp:** every refusal mcp serve makes before it serves is coded, in the format asked for ([f5c2a3a](https://github.com/this-is-tobi/rta/commit/f5c2a3a7bf4c10d700090aa0943562d9229a23ad))
* **mcp:** mcp install refuses --global for a client it cannot scope as core.usage, in -o's format ([2b25716](https://github.com/this-is-tobi/rta/commit/2b2571677fd8a9d0d3e346eb681a4deaa6be6726))
* **net:** a port that said nothing answers with an empty response, and says why on a screen ([5bb95f7](https://github.com/this-is-tobi/rta/commit/5bb95f71b92c68410c9e3ce8600caa162076cd44))
* **net:** net send met by silence says to wait longer, not to run net send again ([269b19c](https://github.com/this-is-tobi/rta/commit/269b19c30a1594e5cc9d0d12084d88657273c44f))
* **note:** note show of an empty note has an empty body, and says so on a screen ([4d46e66](https://github.com/this-is-tobi/rta/commit/4d46e66ca8b4ad21792bc6fed45085edbaab6864))
* **plugin:** a bound declared as 1e6 is written 1000000, the way it is typed back ([ec48f83](https://github.com/this-is-tobi/rta/commit/ec48f83889a7d81c83be3e7d2761f6854c2c3eb4))
* **plugin:** a config refusal names the heading it was read under, and an override that exists ([8f60de4](https://github.com/this-is-tobi/rta/commit/8f60de47bfcce3cdb4ac5178167a264f5b70c0bb))
* **plugin:** a default outside its own range fails validation ([8b3177f](https://github.com/this-is-tobi/rta/commit/8b3177ffe66bf6cbe35d23e85e9ceabb524a4994))
* **plugin:** a fraction is not read as a whole number, from a file as from a flag ([2490b83](https://github.com/this-is-tobi/rta/commit/2490b835b5d65bf489d1527300006629415197ac))
* **plugin:** a key several capabilities read is held to every one of them ([5a32d78](https://github.com/this-is-tobi/rta/commit/5a32d786ac0cf4bafa419ac70ab0613a38a3f042))
* **plugin:** a key written with no value is reported as setting nothing ([475d219](https://github.com/this-is-tobi/rta/commit/475d219ca3b00a81897860770c545ad091e66217))
* **plugin:** a number from config or a profile is held to each capability's range ([26ce22a](https://github.com/this-is-tobi/rta/commit/26ce22aff67717828fb2773987624b40e739a3e0))
* **plugin:** a number no accessor can read is refused, not run as zero ([d72fd6e](https://github.com/this-is-tobi/rta/commit/d72fd6e4efdc6672e31d69df076f05788536868f))
* **plugin:** a number written as text is hinted with a number its input takes ([ef12037](https://github.com/this-is-tobi/rta/commit/ef12037e17929400172ee7d673b743dc1fce41ef))
* **plugin:** a number written as text is refused as text, and told how to write it there ([9a31d5c](https://github.com/this-is-tobi/rta/commit/9a31d5c848cc97fa17d8dc48f9afe6b7a16981e4))
* **plugin:** a Piped input with Options is refused, since the piped text never meets the set ([e4b01b0](https://github.com/this-is-tobi/rta/commit/e4b01b02d95662fa436994c608c60cc149ab09fe))
* **plugin:** a required input with no value is refused before the handler, on every surface ([820759e](https://github.com/this-is-tobi/rta/commit/820759ea5fed26d1cedbb2c9fe9d44c17d77f397))
* **plugin:** a value of a shape its input cannot read is refused, whatever its type ([2428171](https://github.com/this-is-tobi/rta/commit/2428171e7fa75a685295d47c7057121164973000))
* **plugin:** a whole number json spells as 5.0 or 1e2 is read as one, however it was decoded ([165e228](https://github.com/this-is-tobi/rta/commit/165e228975294f26f79712ed284e011b7e732afc))
* **plugin:** an integer input's min and max are whole numbers ([d3af8f7](https://github.com/this-is-tobi/rta/commit/d3af8f77ece05b2261c797ef0a4ba6aad813054e))
* **plugin:** an out-of-range number is refused, not moved inside the range ([793f335](https://github.com/this-is-tobi/rta/commit/793f3351aefa672880bdbd75bc577afa1dc465da))
* **pluginhost:** a plugin's stderr reaches the terminal with every character it acts on escaped ([c2162d3](https://github.com/this-is-tobi/rta/commit/c2162d38e1a5b4c1795f6a8dda7b3ebba86ca0f9))
* **plugin:** options are declared on text alone, and a number bounds itself with min and max ([6d1d70a](https://github.com/this-is-tobi/rta/commit/6d1d70aa6adbc558f1afc65f6083e8037f2271a9))
* **plugin:** options are held on every input type, in the spelling they are declared ([3f104bf](https://github.com/this-is-tobi/rta/commit/3f104bffe232db9dd76cb0432312278ee1af5bcb))
* **plugin:** plugin trust with nothing waiting answers with its table, and says so on a screen ([f619bb3](https://github.com/this-is-tobi/rta/commit/f619bb36ee021f26849393a0728cc599e0a603a3))
* **plugin:** the conformance suite holds inputs to the check the host runs ([02367cd](https://github.com/this-is-tobi/rta/commit/02367cd411267b05f60e4d5eda88220b99a36b1a))
* **plugin:** the host holds every value to its field's Options, on every surface ([f19f766](https://github.com/this-is-tobi/rta/commit/f19f76646c29c11f75f37d6acf521737614dce26))
* **policy:** policy init and require refuse with a code, in the format asked for ([3fcf3d7](https://github.com/this-is-tobi/rta/commit/3fcf3d7ce61a275462623bc3fef6ce5d7df767da))
* **profile:** a colour that is not a colour is noted, and the profile stays usable ([a3210dc](https://github.com/this-is-tobi/rta/commit/a3210dcd4c4cafcc01aee1c8d16b8c5622f53242))
* **profile:** a profile's set: is held to every capability reading its key ([a51009a](https://github.com/this-is-tobi/rta/commit/a51009ae4558fdcbabeafc0b6482054896dbc7bf))
* **profile:** a set number no capability accepts is noted by profile list, not called ok ([3010cd8](https://github.com/this-is-tobi/rta/commit/3010cd83bb54b642585f7fd16978edc1ef046e27))
* **profile:** an empty profile list carries its sentence on the table, like every listing ([d6781c2](https://github.com/this-is-tobi/rta/commit/d6781c24c54ae52d62b14210dcf109123673bf56))
* **profile:** an empty profile list says what would fill it, and stays a table to a parser ([005f074](https://github.com/this-is-tobi/rta/commit/005f07429e11f01aad47b948517054aabf0a181c))
* **profile:** profile set --dry-run previews a new profile with the provenance its write gets ([26faf66](https://github.com/this-is-tobi/rta/commit/26faf663a135de700ac63e854719419f927004f4))
* **profile:** profile set --dry-run says it would create or update, not "would created" ([24d5922](https://github.com/this-is-tobi/rta/commit/24d5922b5eaf232cc91b57b525495d044db42932))
* **profile:** profile set hints a value the key takes, and names the key as it was typed ([647b73a](https://github.com/this-is-tobi/rta/commit/647b73aaccaf16df1e51a204cc946edea3b2a20c))
* **profile:** profile set refuses a value every call through the profile would refuse ([6bdfc80](https://github.com/this-is-tobi/rta/commit/6bdfc80ff166cb90cc76fe3ca9bfef770372e572))
* **profile:** use --off says what it switched off ([1dccc45](https://github.com/this-is-tobi/rta/commit/1dccc4531ff1405160c3bf8b500d68297122cf4c))
* **sdktest:** a default the host reads is not reported as a conformance failure ([cec0a4e](https://github.com/this-is-tobi/rta/commit/cec0a4e20dde6c42365f8ba6ba81463ab03d1721))
* **sdktest:** a plugin's views are rendered as a terminal draws them, not only into a pipe ([32793cf](https://github.com/this-is-tobi/rta/commit/32793cf1d80a70788922594d234718f4987a2e2c))
* **textclean:** a byte that is not UTF-8 is drawn as U+FFFD, not passed on as it came ([31da5a7](https://github.com/this-is-tobi/rta/commit/31da5a7c8d490e9852d7bf0239c615be4c2275fc))
* **textclean:** a character that reorders text is spelled out where a person reads it ([3ffd18a](https://github.com/this-is-tobi/rta/commit/3ffd18abc503f50a0d08dfa79dd1303007288609))
* **time,agent:** a date that does not exist says which part of it does not ([9389a06](https://github.com/this-is-tobi/rta/commit/9389a061b900423907a9f005d2469a363cc10a86))
* **toolcall:** an enum miss is hinted with the enum, not with the type ([95e5a88](https://github.com/this-is-tobi/rta/commit/95e5a8899c832f8a0f2ad082cb4f087d9903fa45))
* **tui:** a form box holds a number to its range as it is typed ([9fce4d8](https://github.com/this-is-tobi/rta/commit/9fce4d816cc68da124cd251caf60314c989d1c84))
* **tui:** a picker keeps a config value outside its options, so the run is refused as on the CLI ([41d13ff](https://github.com/this-is-tobi/rta/commit/41d13ff47468a07650b98d8b4bebfc78d08d44f3))
* **tui:** a profile naming an installed, unapproved plugin says to trust it, not that it is missing ([3457630](https://github.com/this-is-tobi/rta/commit/3457630adf9374013c0ea6fd3e88859adeff8cb6))
* **tui:** a refused config value names the pinned heading it was read under, as the CLI does ([6ee5e02](https://github.com/this-is-tobi/rta/commit/6ee5e02e48fa1fde9b192196b67e22e8b0de7479))
* **tui:** a required list picked from options is not submitted with nothing picked ([4fc3621](https://github.com/this-is-tobi/rta/commit/4fc3621461863b66344ef920a959da5823e2eb81))
* **tui:** a TUI stopped by a signal exits cleanly, and any other failure is coded ([4658d51](https://github.com/this-is-tobi/rta/commit/4658d51ef1f6f7f89b2dca70301fa86a9361ff20))
* **tui:** an empty listing's pane is headed by its sentence, not by "0 of 0 rows" ([de08563](https://github.com/this-is-tobi/rta/commit/de085639a8948e9b2a5e59cc5a432f512e887042))
* **tui:** the footer draws a flash cleaned, and cluster completion offers no name that deceives ([ae99928](https://github.com/this-is-tobi/rta/commit/ae99928fe6a9401b08db88c628a17a0a53ab6179))
* **tui:** the footer says remove over a tile H would withdraw, not hide ([c1c611f](https://github.com/this-is-tobi/rta/commit/c1c611f4b546fab486cb78fb0783e568d5505872))
* **tui:** the profile panes draw what the config file says cleaned, under a band as in its name ([f443a0c](https://github.com/this-is-tobi/rta/commit/f443a0c553a676e5d75569a3d15adb16cece0374))
* **tui:** the profile panes mark warn and say why an environment runs otherwise than written ([7e8a5d1](https://github.com/this-is-tobi/rta/commit/7e8a5d18d8e65ef7a88f3f5478a3198d68094715))
* **view:** an empty collection in json, yaml and MCP output is [], never null ([2f7be41](https://github.com/this-is-tobi/rta/commit/2f7be414d0df4310cee73d2c7e078b76107259fc))
* **view:** json output escapes what a terminal acts on and encoding/json leaves raw ([5af97e7](https://github.com/this-is-tobi/rta/commit/5af97e7141b5c354430a1a4f2c96e3a6aee40463))
* **view:** json output keeps &lt; &gt; and & as they are written, at every depth ([291d77c](https://github.com/this-is-tobi/rta/commit/291d77c64356728e3b12ac8732c915a5bcccc1f9))


### Code Refactoring

* **format:** one byte count, whatever integer the caller is holding ([b672c31](https://github.com/this-is-tobi/rta/commit/b672c31282abafe67190f5c932fe10ed1fd0973f))
* **kv:** the recipient comparison is slices.Equal ([fd12c07](https://github.com/this-is-tobi/rta/commit/fd12c077f98ee23a9e7569f76d0f7b6f2e8be01e))


### Documentation

* **cli:** the output formats say json is the one exact format ([0fbe689](https://github.com/this-is-tobi/rta/commit/0fbe68956c16500e773ef5d75aeae83efddd9ccc))
* **install:** an upgrade is checked with rta doctor, which names what the new binary refuses ([30f5cbb](https://github.com/this-is-tobi/rta/commit/30f5cbb6e93c3c2d44eccd76973b7ad7a9c5991a))
* **profiles:** the refusal lists name the option and range checks, with their codes ([fec1ccc](https://github.com/this-is-tobi/rta/commit/fec1ccc8fcfb6945d02180651e770e6417a97d07))

## [0.26.0](https://github.com/this-is-tobi/rta/compare/v0.25.0...v0.26.0) (2026-09-22)


### Features

* **plugin:** `rta plugin prune` drops the stored versions nothing runs ([e892971](https://github.com/this-is-tobi/rta/commit/e892971b3e02c3ebffae612a99f3d3e8e244eda3))
* **sdk:** plugin.ExpandHome, so a plugin need not carry its own tilde rule ([2661ac6](https://github.com/this-is-tobi/rta/commit/2661ac67870b77c5c92fc40f5ead8b797f527b45))
* **tui:** + on a catalogue row or a search match adds the tile, asking which connection ([12d15c5](https://github.com/this-is-tobi/rta/commit/12d15c5f38865e79c84cb1e402e859f2c0b9a1b6))


### Bug Fixes

* **atomicfile:** wait out a read Windows refuses while another handle is on the file ([e3f9bcc](https://github.com/this-is-tobi/rta/commit/e3f9bcc98af99b4f8ec50994e4302f60ba9d7d21))
* **audit:** bound the kube audits' wait on kubectl's pipes, and never print a base URL's credential ([97e24d3](https://github.com/this-is-tobi/rta/commit/97e24d3f345eba1cac162382632411868a9d34bf))
* **git:** a changed file over sixteen megabytes is named in the diff rather than read whole ([ec0ead3](https://github.com/this-is-tobi/rta/commit/ec0ead35300cd10cd81ae6e4e1256dd076e4a51e))
* **keys:** a comment may not carry a line break, and a piped phrase is read up to a bound ([f18802c](https://github.com/this-is-tobi/rta/commit/f18802ced1fae5561070b3e8bcbaf59e5a085c97))
* **pkg:** go tools under cmd/ are checked against their module, an orphan cannot wedge a listing ([47e60b0](https://github.com/this-is-tobi/rta/commit/47e60b08dc88ac0aee7d7e7e4c78c7254d2e5adc))
* **plugin:** the hint about further unreadable indexes counted them twice ([72586af](https://github.com/this-is-tobi/rta/commit/72586af0f9e9475104970138036940c982f5d137))
* **tunnel:** name a missing credential plugin, end a cancelled forward gently, keep -- on listings ([01bf6ab](https://github.com/this-is-tobi/rta/commit/01bf6ab4993f0f3d25b5301438f4d34fbce7a9d4))


### Performance Improvements

* **pluginhost:** hash every installed plugin at once, so a dozen cost one ([6870333](https://github.com/this-is-tobi/rta/commit/6870333d8638e96837a98367eba6f2f68544a5e6))

## [0.25.0](https://github.com/this-is-tobi/rta/compare/v0.24.0...v0.25.0) (2026-09-21)


### Features

* **cli:** rta dashboard add, rm and list write the tiles the automatic set leaves out ([e838614](https://github.com/this-is-tobi/rta/commit/e838614993033bac38dbe54a5734cc8a2c5dbd27))
* **cli:** rta dashboard hide and unhide, and add expands over a profile's connections ([9bd08c7](https://github.com/this-is-tobi/rta/commit/9bd08c73f79bcce57c4f8cef6b70a3c01e31d18f))
* **config:** add: joins tiles to the automatic set, and a tile pins to a profile ([036e876](https://github.com/this-is-tobi/rta/commit/036e87635eef359962a1bec838dd374599ed90c9))
* **tui:** a pinned tile runs against its own profile, named on its panel ([6ec25ab](https://github.com/this-is-tobi/rta/commit/6ec25abfef27534a9da4cbb9935d49a5b3059c36))
* **tui:** a tile over a profile with several connections is one panel per connection ([9ed1157](https://github.com/this-is-tobi/rta/commit/9ed1157c54e9a5dc1bada1799b6974cb6ebd0b03))


### Bug Fixes

* **cli:** dashboard hide covers an expanded automatic tile, add refuses a twin of one already there ([af9e065](https://github.com/this-is-tobi/rta/commit/af9e0653396cd02bd5281d5372888430060a3d60))
* **tui:** an expanded entry moves as one, hides on a stated list, and takes only its own answers ([b8828a8](https://github.com/this-is-tobi/rta/commit/b8828a87484d6858f737bdbc9753a2bf00c42a46))
* **tui:** the tick reads the config once for every pin, and a pinned profile that changed rebuilds ([1bd0263](https://github.com/this-is-tobi/rta/commit/1bd0263832d63c4d9b8d8e9d73de4868e7a884da))

## [0.24.0](https://github.com/this-is-tobi/rta/compare/v0.23.0...v0.24.0) (2026-09-20)


### ⚠ BREAKING CHANGES

* **eol:** eol.watch entries are product@cycle, and product/cycle, which earlier releases accepted, is refused with the entry named. A `plugins: eol: products:` list needs its slashes replaced by @.

### Features

* **audit:** audit.kube.eol grades a cluster's versions against endoflife.date ([b014d83](https://github.com/this-is-tobi/rta/commit/b014d83d966742c2ee6d0317154f637b82f04ad6))
* **eol:** a cycle can be a range, and a watch entry is product@cycle ([ba5a84f](https://github.com/this-is-tobi/rta/commit/ba5a84fa1e60499f6d4c3d323f9d0b2356c711f8))
* **explain:** the card says what the dashboard does with a capability ([be12236](https://github.com/this-is-tobi/rta/commit/be1223688fe69311a7b4283e2d9caa5a9f0f390b))
* **tui:** a capability declares how often its dashboard tile re-runs ([1d13e15](https://github.com/this-is-tobi/rta/commit/1d13e15e671730a77b82d29cf965004e398d66e6))


### Bug Fixes

* **audit:** an end-of-life date already behind us is said as such ([cca907a](https://github.com/this-is-tobi/rta/commit/cca907a37f78b021c5b861009ac6acfbb1d00c0e))
* **audit:** audit.kube.eol grades every row it reads, floating variants included ([b05a5cd](https://github.com/this-is-tobi/rta/commit/b05a5cd65c1de12bdfc813855cc8c59fb2d1f856))
* **cli:** a go-installed rta reports the module version, not dev ([01fb844](https://github.com/this-is-tobi/rta/commit/01fb8444074f9ffabaed5560a1707c59e857f61a))
* **cli:** a go-installed version compares equal to the archive's stamp ([6192cbb](https://github.com/this-is-tobi/rta/commit/6192cbbc6e689b24eb420e2b40e3ea33cfa32a26))
* **eol:** a cycle can be named by its codename ([7eaae64](https://github.com/this-is-tobi/rta/commit/7eaae644a3f311d6ddf8a5339e53ed06b0b88af3))
* **eol:** a watch entry in the old product/cycle spelling is refused, naming the new one ([20af09d](https://github.com/this-is-tobi/rta/commit/20af09d7157167132bd9edbe7bdd67418b4bbe7e))
* **eol:** eol check takes product@cycle, and a watch entry's product is trimmed ([03a4a97](https://github.com/this-is-tobi/rta/commit/03a4a9789353087aa25fa2944c59b87ca63e3a03))


### Code Refactoring

* **eol:** the endoflife.date client moves to a shared builtin package ([dc60325](https://github.com/this-is-tobi/rta/commit/dc60325ade339c5c7d0592ce85c0a734dbffdc1c))

## [0.23.0](https://github.com/this-is-tobi/rta/compare/v0.22.0...v0.23.0) (2026-09-20)


### Features

* **view:** a Table can say what it could not cover ([914c95c](https://github.com/this-is-tobi/rta/commit/914c95c41acb668864df849f6b3c297f5c8bc787))


### Bug Fixes

* **agent:** a dashboard that could not read something says so, rather than reporting nothing ([7f2e2f6](https://github.com/this-is-tobi/rta/commit/7f2e2f620c1b84fd6447d2aec833bfd587085c50))
* **agentlog:** the record that a segment was retired reads its own close ([5eb5658](https://github.com/this-is-tobi/rta/commit/5eb56589dddf0283d2ece0fd55a22b648e6258be))
* **audit:** a check that could not run is reported, instead of grading what it saw ([e89d973](https://github.com/this-is-tobi/rta/commit/e89d97353a9f4a15174c643c73e152c8f39fbaf8))
* **builtin:** six reads that could not read said nothing about it ([15d19f1](https://github.com/this-is-tobi/rta/commit/15d19f1ddbd8f832b921a266a6c33b2978195a62))
* **cli:** a count of one prints a singular noun, wherever the count is live ([870537c](https://github.com/this-is-tobi/rta/commit/870537c10ce680f1ef9f0e438b0de1be96c4b7e7))
* **cli:** a typo is answered in one sentence, and every extra argument is named ([3c60ddf](https://github.com/this-is-tobi/rta/commit/3c60ddf35faf385c3b1d5578737e765868c358bb))
* **doctor:** a profile problem is reported once, naming everywhere it applies ([1232430](https://github.com/this-is-tobi/rta/commit/1232430dfbdfb0e0f6d87dc4448a921c4395437e))
* **doctor:** say when a connected server is running another build ([cf670ae](https://github.com/this-is-tobi/rta/commit/cf670aebd326e92136904ee17d6534818aa33640))
* **git:** a repository whose packs this reader skips is refused, not guessed at ([90005e2](https://github.com/this-is-tobi/rta/commit/90005e20c684a6ab3925f9d96ded563bd684e7df))
* **grant:** allow says when an open server is on another build ([5cdd15a](https://github.com/this-is-tobi/rta/commit/5cdd15aa29ced585b55009fa1eb735203904adce))
* **grant:** an unknown role is answered the same way on every surface ([143ab77](https://github.com/this-is-tobi/rta/commit/143ab7796891c1df6a86aa20643480f7393dc1bb))
* **kv:** recipients says there is no store, instead of describing one that is not there ([df49407](https://github.com/this-is-tobi/rta/commit/df494078281594044bf60461148ecd725c7f75b3))
* **mcp:** the record says when a refusal was a connection that moved ([158c3e7](https://github.com/this-is-tobi/rta/commit/158c3e74193b5242df6dc9905f96c2dd1b0c7189))
* **paths:** a bare ~ resolves everywhere, not in three commands out of five ([f2a6e20](https://github.com/this-is-tobi/rta/commit/f2a6e20b035bb209dbad485327ba2ad3610e756c))
* **pkg,git:** five reads that could not compare said they had ([47ac7ad](https://github.com/this-is-tobi/rta/commit/47ac7ad7811a3498c2ef123959e1b617f2e1b7c0))


### Code Refactoring

* **format:** one vocabulary for counting, instead of nine copies and four meanings ([6e837c0](https://github.com/this-is-tobi/rta/commit/6e837c066b824f2662eba048a0c13e18ee21dd75))
* the thirteen hand-rolled slices.Contains become slices.Contains ([7e70571](https://github.com/this-is-tobi/rta/commit/7e70571940b336a40e8aaaa27993e0c775f9a288))

## [0.22.0](https://github.com/this-is-tobi/rta/compare/v0.21.1...v0.22.0) (2026-09-19)


### Features

* **audit:** grade a DKIM key's strength and its testing flag ([395a7e2](https://github.com/this-is-tobi/rta/commit/395a7e2bc33957e53f9e07f9c634b9595fe1eea4))
* **plugin:** a capability declares what the TUI may do with its result ([529dd9b](https://github.com/this-is-tobi/rta/commit/529dd9b9fc498805a862c321f92d65caf5d7c013))
* **plugin:** explain prints the declared actions, and sdktest checks Copy against the view ([72fe9e5](https://github.com/this-is-tobi/rta/commit/72fe9e5f6c0eae80b0563219bdb36577fc2f854e))
* **tui:** ? lists every key the screen answers ([aa6bac4](https://github.com/this-is-tobi/rta/commit/aa6bac4657a29d6a143073ce64c320c8b76c0aca))
* **tui:** row actions, toggles, copy and live refresh come from the declaration ([dba0c65](https://github.com/this-is-tobi/rta/commit/dba0c65f7242c0cb2498fc2c3fb6d51a76cf2b64))


### Bug Fixes

* **atomicfile:** a link that could not be made is not a lost race ([06f7dc8](https://github.com/this-is-tobi/rta/commit/06f7dc8f6013fa63ae732a47ff8901a22b20d93b))
* **atomicfile:** wait out a platform that will not let a file be replaced ([00ec023](https://github.com/this-is-tobi/rta/commit/00ec02305fb26e59cfefff078fea803f663d29b1))
* **audit:** a missing DMARC record names the parent policy it may inherit ([d287753](https://github.com/this-is-tobi/rta/commit/d287753dd69ba8cc7ea9ae06f812193a069dd383))
* **audit:** a TLS-RPT record with no rua= reports to nobody ([a3afc63](https://github.com/this-is-tobi/rta/commit/a3afc63bb220d4244a1eb53a0f0298e5b2af85b3))
* **audit:** an empty DMARC pct= is a syntax error, not the default ([c6bbf7d](https://github.com/this-is-tobi/rta/commit/c6bbf7d174d4b2e0cf9f9986f1b7499e7e2ee0d7))
* **audit:** an MTA-STS TXT record advertises a policy it does not prove ([2862604](https://github.com/this-is-tobi/rta/commit/28626049dfd8ee504cd0f404c12d1e12c61fdd22))
* **audit:** hold a mail domain to the same DNS label grammar as its selector ([71b042c](https://github.com/this-is-tobi/rta/commit/71b042cbdb335d8b43728ef92bcb7a26ddfe0aa7))
* **audit:** podsecurity grades init and ephemeral containers too ([66e83f1](https://github.com/this-is-tobi/rta/commit/66e83f168505b615cb53e411a3a4a5091b8ae680))
* **audit:** read one DKIM key per TXT record, and say when a selector holds two ([ef1fe03](https://github.com/this-is-tobi/rta/commit/ef1fe03c1e6ea35779865a1b43d3715e5c22b5c1))
* **cli:** name a credential's environment variable from the declaration ([975241e](https://github.com/this-is-tobi/rta/commit/975241ec0a11efee0321223cf4f154d7037bdeac))
* **docker:** the full image's Alpine base moves to 3.22.6, which patches openssl ([8311461](https://github.com/this-is-tobi/rta/commit/8311461bdd5a313d900bb73ce6bdb90eb116fcd1))
* **doctor:** name a seal key left with no lock file beside it ([8016153](https://github.com/this-is-tobi/rta/commit/80161531c619b8ffe9836839124964bef4fe21b9))
* **filelock:** a missed beat costs a beat, not the lease ([84c65b1](https://github.com/this-is-tobi/rta/commit/84c65b12fc466a3227603d7fff246690dc7c1f43))
* **filelock:** wait out a platform that will not let a lock go ([9d29127](https://github.com/this-is-tobi/rta/commit/9d291276c1f32b6d722c60af5cc2a85d39985f7d))
* **git:** blame and log take a file the way the boundary hands it, relative to the repository ([ddcbe88](https://github.com/this-is-tobi/rta/commit/ddcbe8866be69cd248d7b039d2750499f2436486))
* **kv:** notepad is the editor Windows guarantees ([536e783](https://github.com/this-is-tobi/rta/commit/536e7837bbbec5bd9cf6ca48699e66c28e92bdca))
* **lock:** a mutation that changes nothing writes nothing ([ec36b5d](https://github.com/this-is-tobi/rta/commit/ec36b5df832916670ff66a8c0c2cba51512a4c6a))
* **lock:** a remote add confirms what the operator typed, not the server's answer ([d021306](https://github.com/this-is-tobi/rta/commit/d021306dbefabecee9896290c2d1d89fd2c41da4))
* **lock:** a remote typo is refused before the passphrase ([b5b2f25](https://github.com/this-is-tobi/rta/commit/b5b2f250edb6c0118182d1b19e0db340b671fba0))
* **lock:** a truncated seal key names the file that fixes it ([0e0c9b9](https://github.com/this-is-tobi/rta/commit/0e0c9b9b43758deb8fc2132bd5b1600e54f36302))
* **lock:** the recovery hints paste on Windows, and an empty principal is refused early ([d44ce68](https://github.com/this-is-tobi/rta/commit/d44ce68fe6ee71c5c1019d947a86241681566d82))
* **net:** net.trace keeps the hops it collected when the run is stopped ([2b3ea3e](https://github.com/this-is-tobi/rta/commit/2b3ea3e85ab40eaa046bf46b059269c84a892090))
* **pkg:** pkg.upgrade keeps the manager's output off the screen the TUI draws on ([adcd53b](https://github.com/this-is-tobi/rta/commit/adcd53b03a600fb4b43fca788ab1d7b3c355df1d))
* **plugin:** an integer past what fits is refused, and every narrowing conversion is bounded ([e09e23f](https://github.com/this-is-tobi/rta/commit/e09e23f6dea234fc2f729915a907926f51e97ce6))
* **plugin:** state an input's help without naming the surface it arrives on ([a2875b4](https://github.com/this-is-tobi/rta/commit/a2875b4d52609534d3ede854136472a84684b0ba))
* **render:** break a long identifier after a separator, not mid-group ([5f23392](https://github.com/this-is-tobi/rta/commit/5f233926e259d51e4f7855cffdd905abac38746a))
* **seal:** an unreadable seal key is a read failure, not a missing key ([7bb7875](https://github.com/this-is-tobi/rta/commit/7bb7875981b0552fa04c7b369b12cab74b051acd))
* **tui:** a row taller than the screen is drawn at the height that fits ([ce0c002](https://github.com/this-is-tobi/rta/commit/ce0c00260983547d2bdfe750b86204d8436d4194))
* **tui:** keep a ceiling on a run that opened a port-forward ([05b6dd0](https://github.com/this-is-tobi/rta/commit/05b6dd0f820330187631e83fac5cc95e01b90934))
* **tui:** name the dashboard's own deadline when a tile stops answering ([a2c07a1](https://github.com/this-is-tobi/rta/commit/a2c07a195e376cb9c596555422292e691db5288d))
* **tui:** paint nothing until the terminal size is known ([a140a66](https://github.com/this-is-tobi/rta/commit/a140a66ff7e05a0086f98d44dc4b4899eb06c9eb))
* **tui:** path completion splits on either separator on Windows ([85d3b7a](https://github.com/this-is-tobi/rta/commit/85d3b7a44f126899547c8c23b6442e6df6d9fbb4))
* **tui:** quit cancels the run from every screen, and the deadline paths are pinned ([b86ebb4](https://github.com/this-is-tobi/rta/commit/b86ebb457f36e0869ae0d817b311b8c8fc7df066))
* **tui:** re-window the dashboard when a tile's answer changes its row height ([c82a966](https://github.com/this-is-tobi/rta/commit/c82a96613e61c462e4a355fe88fcb75f203e6007))
* **tui:** stop cutting an asked-for capability run off at thirty seconds ([4337a19](https://github.com/this-is-tobi/rta/commit/4337a19dc9e710db94263dfcc9995d26282c89ea))
* **tui:** stop promising that esc leaves a run running ([93fd98b](https://github.com/this-is-tobi/rta/commit/93fd98bbb383148bbdbe459c2f437107734c15bd))


### Code Refactoring

* **audit:** decide a mail domain exists from the lookups already made ([1a4a95e](https://github.com/this-is-tobi/rta/commit/1a4a95e3ec0a5bb4ad185aa47b8270f59b73d927))


### Dependencies

* **goreleaser:** the release does ship an SBOM, and the footer says when it applies ([5ca78c2](https://github.com/this-is-tobi/rta/commit/5ca78c250dac1421f0abcdad233124e3253f3cf9))
* **make:** chart-docs runs a helm-docs built from source at a pinned version ([92cb637](https://github.com/this-is-tobi/rta/commit/92cb637ac5b4b2444409afe0b1ac6c3ee7499826))
* **make:** guard bump-index's ref, and pin chart-docs to the helm-docs digest ([557ac27](https://github.com/this-is-tobi/rta/commit/557ac27ab4e08536479fe6fa69828b23b93ee473))

## [0.21.1](https://github.com/this-is-tobi/rta/compare/v0.21.0...v0.21.1) (2026-09-16)


### Bug Fixes

* **audit:** audit.mail queries the domain its argument reads as ([584bf12](https://github.com/this-is-tobi/rta/commit/584bf126bc0dffbc832801fd56d68f38cc976ed1))
* **audit:** audit.mail stops reporting three false all-clears ([62afdee](https://github.com/this-is-tobi/rta/commit/62afdee41806d138707ecf7b57b46af9fc336c36))
* **deps:** bump grpc-go past the HIGH severity CVE-2026-84445 ([413c690](https://github.com/this-is-tobi/rta/commit/413c6903c5ebda30f279283a61d7609d57ce1955))
* **tui:** a row action keeps the aim of the listing it was pressed in ([bbc8912](https://github.com/this-is-tobi/rta/commit/bbc89129157d1419afaa2f9acc8eb2eb89d0b297))
* **tui:** the dashboard stops advertising a tile action its own keys shadow ([420cdf6](https://github.com/this-is-tobi/rta/commit/420cdf625aef1b3a7bfd29a37eb7bde9fd1a1f24))

## [0.21.0](https://github.com/this-is-tobi/rta/compare/v0.20.0...v0.21.0) (2026-09-15)


### Features

* **plugin:** refuse an upgrade that widens what a plugin may do ([093189b](https://github.com/this-is-tobi/rta/commit/093189b0d734a7e0d6abc216db1f2f709bfcd925))
* **plugin:** sweep every plugin with --all on upgrade, untrust and remove ([7d08b67](https://github.com/this-is-tobi/rta/commit/7d08b6713f19567658f55c245d46a2705fd3cf2f))


### Dependencies

* **docker:** the full image builds from the index's newest commit ([6c648a1](https://github.com/this-is-tobi/rta/commit/6c648a160a5601e233cfce4f7a850f623aac8003))

## [0.20.0](https://github.com/this-is-tobi/rta/compare/v0.19.0...v0.20.0) (2026-09-14)


### Features

* **chart:** the full image needs nothing copied into the data volume ([27f78c5](https://github.com/this-is-tobi/rta/commit/27f78c53a875fb3d2ade1021871397a4410a100c))
* **docker:** the full image installs its plugins from the official index ([2905e53](https://github.com/this-is-tobi/rta/commit/2905e53a17ed47b71c9ec86c8cebee970023fc70))
* **plugin:** a read-only system root for what an image or a package installed ([b753415](https://github.com/this-is-tobi/rta/commit/b753415958c4759678b0230a0506e7bfb1fe6d07))
* **plugin:** an index attaches pinned at a ref ([ef72f9d](https://github.com/this-is-tobi/rta/commit/ef72f9ddad86126446ae2046d5a43c7776adf4a3))

## [0.19.0](https://github.com/this-is-tobi/rta/compare/v0.18.0...v0.19.0) (2026-09-14)


### Features

* **cli:** group the root help by what a command is for ([e33392c](https://github.com/this-is-tobi/rta/commit/e33392ca164bbee0895d8181dd34231e56cd81a8))
* **tui:** a destructive run is confirmed on what it would do ([8c8b724](https://github.com/this-is-tobi/rta/commit/8c8b7245bed2205a14d143a0e095e9a3047cbb1b))


### Bug Fixes

* **cli:** find a view error through wrapping ([05aaa3e](https://github.com/this-is-tobi/rta/commit/05aaa3e694401d974e3c4c7958ebf025a77ad9f5))
* **cli:** suggest the nearest verb below the root too ([e3b5b88](https://github.com/this-is-tobi/rta/commit/e3b5b8855e62e184da38d7d0d5b51d488423e7af))
* **mcp:** state the request body limit, and bind listeners under the command's context ([f93e852](https://github.com/this-is-tobi/rta/commit/f93e85220853cbbbc677dfb6637bf8f4e9f98808))
* **mcp:** the path gate covers rta's configuration as it covers its state ([547923b](https://github.com/this-is-tobi/rta/commit/547923bad7d787fcd528b8bfd94b2f873dac437c))
* **paths:** create the data directory owner-only, from one place ([ed661b9](https://github.com/this-is-tobi/rta/commit/ed661b99a17bd69d8b8f75d42b50da467ae8f5cc))
* **plugin:** scaffold against the released SDK, not a checkout ([af8eeb2](https://github.com/this-is-tobi/rta/commit/af8eeb230ea4200511a450f8fdede6a59ea2a802))
* **sys:** read cpu usage from the per-core counters, never a frozen total ([2363200](https://github.com/this-is-tobi/rta/commit/2363200514e3f6d60297b22aaba9d3b2dd040e54))
* **tui:** a launched search does not follow you back ([5f33b9c](https://github.com/this-is-tobi/rta/commit/5f33b9c983dcffe2ecc0cc175891844759c40345))
* **tui:** one help bar per form, and it says why a field refused ([03475dc](https://github.com/this-is-tobi/rta/commit/03475dc80bf492aa22cfb0f07eee25f1d41c37c4))
* **tui:** the catalogue keeps its permission column at eighty columns ([d7e7233](https://github.com/this-is-tobi/rta/commit/d7e723378c990fff0afe49218e7c2599f63f5504))


### Code Refactoring

* **doctor:** one function per group of rows ([dbcabe9](https://github.com/this-is-tobi/rta/commit/dbcabe987fe73fd6fec9f4200b88d86859911533))
* **operator:** a remote call carries the command's context ([7e87900](https://github.com/this-is-tobi/rta/commit/7e87900bff2a4ca0196fc2720dcedf7c5e8a8670))
* remove code nothing calls, and comments about milestones that shipped ([cffc6bc](https://github.com/this-is-tobi/rta/commit/cffc6bc0bcd0fa3cd113767c881abc120898bb2f))


### Dependencies

* a size ceiling the build refuses to exceed ([b61c69a](https://github.com/this-is-tobi/rta/commit/b61c69a58072e046da11010db0cd6e592414ece9))

## [0.18.0](https://github.com/this-is-tobi/rta/compare/v0.17.0...v0.18.0) (2026-09-13)


### Features

* **findings:** a citation links to where its text is read ([4e18a51](https://github.com/this-is-tobi/rta/commit/4e18a518c654223b9ad46c717c1bcadcaaeb182c))
* **tui:** the store asks for its passphrase once per sitting ([b04a2b6](https://github.com/this-is-tobi/rta/commit/b04a2b68aec8f16354f80be0482609366e844982))


### Code Refactoring

* **audit:** lift the findings report into pkg/findings ([6d599d5](https://github.com/this-is-tobi/rta/commit/6d599d52e4cda5b8cf04516f2eea60da60bacd38))

## [0.17.0](https://github.com/this-is-tobi/rta/compare/v0.16.0...v0.17.0) (2026-09-11)


### Features

* **cd:** publish and attest the chart on every release ([06c9e42](https://github.com/this-is-tobi/rta/commit/06c9e4202f3f1c65cd7a3fea543c49368aa1851b))
* **chart:** a Helm chart for rta MCP servers, one instance per person ([e59ccb7](https://github.com/this-is-tobi/rta/commit/e59ccb7cd50427da178d3c9c49030286ef331b87))

## [0.16.0](https://github.com/this-is-tobi/rta/compare/v0.15.0...v0.16.0) (2026-09-09)


### Features

* **audit:** grade a containerised rta against the documented recipe ([ad91296](https://github.com/this-is-tobi/rta/commit/ad91296adf89bc0f474c13c2c987cf99b102e765))
* **mcp:** probes and counters on a second listener, with --observe ([3d629c9](https://github.com/this-is-tobi/rta/commit/3d629c915fae24dca3e77e1c69042f07c73320f4))


### Bug Fixes

* **app:** --help documents the arguments a command takes ([c2e01d8](https://github.com/this-is-tobi/rta/commit/c2e01d82e078a771a928aa52114c83bcd5f5f90c))
* **audit:** grade a remote MCP server's credential like a local one ([d47984d](https://github.com/this-is-tobi/rta/commit/d47984df421b060173039b770cfd01239b17c7fe))
* **audit:** the same config file is never audited twice ([46bd529](https://github.com/this-is-tobi/rta/commit/46bd529b3881c4d659ccf47562ee3e0ed97f91a5))

## [0.15.0](https://github.com/this-is-tobi/rta/compare/v0.14.0...v0.15.0) (2026-09-08)


### Features

* **app:** add --global to `rta mcp install` ([6f2ce9a](https://github.com/this-is-tobi/rta/commit/6f2ce9af533471c0b262b9d8cb7957172cdd5df3))
* **app:** add `rta profile repin` to re-key stale plugin pins in bulk ([5ffc3f5](https://github.com/this-is-tobi/rta/commit/5ffc3f5e06a7d6f924593f9613b3ed94739a5139))
* **eol:** suggest a product's names and its own release cycles ([095fc27](https://github.com/this-is-tobi/rta/commit/095fc27c49df17c2b933b24a5ecbbec4489522be))
* **lock:** suggest currently-frozen names on lock.rm ([b526860](https://github.com/this-is-tobi/rta/commit/b526860fb5c99522af577fb4d00c58d888aa36c0))


### Bug Fixes

* **app:** --global refuses only where a command would actually run ([e3f9f54](https://github.com/this-is-tobi/rta/commit/e3f9f544c0b12b47effd5b4d12ea6b8f48793e02))
* **app:** mcp install honours --dry-run ([8872026](https://github.com/this-is-tobi/rta/commit/8872026d6e79e37a82741fd229ea2c00ef908f82))
* **app:** plugin install/remove/upgrade/index honour --dry-run ([687f9fe](https://github.com/this-is-tobi/rta/commit/687f9fe1bbd0bfbebf42b727d737b7ec4a1ac393))
* **app:** plugin trust/untrust/allow/disallow/new honour --dry-run ([86690e3](https://github.com/this-is-tobi/rta/commit/86690e35e763eb37525ac5fd2c58d2aabd66f9da))
* **app:** profile set/rm and policy init/require honour --dry-run ([b567bd6](https://github.com/this-is-tobi/rta/commit/b567bd68f7fab9b77f79f7306c1caea66c5f51ba))
* **app:** refuse a profile repin that would fold two entries into one ([e962b1b](https://github.com/this-is-tobi/rta/commit/e962b1b0eeda2e42fc3d9e8e12b5d95ef252c04a))
* **app:** rta use honours --dry-run ([238dc65](https://github.com/this-is-tobi/rta/commit/238dc65ae3c300dcce96a9cf0e969598710219ba))
* **atomicfile:** cap Publish's fallback read, the same as ReadCapped ([1cb2efb](https://github.com/this-is-tobi/rta/commit/1cb2efb532fea4e331cbbd72119ad6864994b9e4))
* **audit:** gate audit.mail the way audit.web already is ([ad7e429](https://github.com/this-is-tobi/rta/commit/ad7e42926d32afb384896992fdb37039d0f15221))
* **filelock:** a stale lock rta cannot remove refuses instead of spinning ([e0c8ac5](https://github.com/this-is-tobi/rta/commit/e0c8ac5e6f5c3f6d5d2bb224b6641eff99378aea))
* **git:** mask a bare-userinfo PAT in a remote URL ([35a7f19](https://github.com/this-is-tobi/rta/commit/35a7f19080dba1b8cae356bba3e884025ff89cdc))
* **grant,plugin:** close the dead-grant scope class at both ends ([c554ae0](https://github.com/this-is-tobi/rta/commit/c554ae0b6371060d6e7cf333c80d1c0c4feebd3c))
* **http:** check the real destination before a proxy can hide it ([8a3f097](https://github.com/this-is-tobi/rta/commit/8a3f09706cd1b09833b86232d6039640ac630687))
* **keys:** cap Publish's fallback read on restored keys, not len(this write) ([525467b](https://github.com/this-is-tobi/rta/commit/525467b046a109002c237886ee171c23157c3b86))
* **net:** gate dns, trace and ping the way probe already is ([594eb3e](https://github.com/this-is-tobi/rta/commit/594eb3e3d136f4758814af4813565027ea917997))
* **plugindist:** resolve reads a manifest through the same guard search uses ([8284257](https://github.com/this-is-tobi/rta/commit/8284257568facd05bd6881ad0ac25aa9bd9190a9))
* **policy:** forbid a namespace grant that contains a forbidden capability ([0d462f5](https://github.com/this-is-tobi/rta/commit/0d462f5be7303655d9325545d3d20a22cbe0fb4a))
* **tui:** a broken profile binding refuses instead of running unprofiled ([1c871f1](https://github.com/this-is-tobi/rta/commit/1c871f1ab761e1bb3169128806a0e1c01247286c))
* **tui:** a run refused by profile resolution renders its refusal ([7101a48](https://github.com/this-is-tobi/rta/commit/7101a48ce31aa37cf35c67eb70783f8bc3c27ec1))
* **tui:** flashText defaults to the generic fallback, not the raw value ([2657af0](https://github.com/this-is-tobi/rta/commit/2657af0190d3bef332e187856c3e4ef574a129a8))
* **tui:** kv.get's reveal lands on its own page, not the flash line ([8509a9e](https://github.com/this-is-tobi/rta/commit/8509a9e3d5759d958254aecd696bb4213c69d856))
* **view:** truncate an over-long table row in Redact, not per-renderer ([d12bcd5](https://github.com/this-is-tobi/rta/commit/d12bcd5960a2ff493bcaf11b35a5ad471de70cc0))


### Dependencies

* **docker:** add make bump-plugins to rewrite the full image's pins ([841e6a2](https://github.com/this-is-tobi/rta/commit/841e6a21c40eed9feabdee1a6267499a91695ec9))

## [0.14.0](https://github.com/this-is-tobi/rta/compare/v0.13.0...v0.14.0) (2026-09-06)


### Features

* **audit:** the environment check is a capability, so doctor is on every surface ([8b19594](https://github.com/this-is-tobi/rta/commit/8b195941829e3f91076f6a715d8663a2676a9acb))
* **kv:** the listing says how many earlier values each entry keeps ([163af18](https://github.com/this-is-tobi/rta/commit/163af18fce72d686376c52e86ce2d88ab3f4b5b5))


### Bug Fixes

* **doctor:** the confinement row names the one readable place inside rta state ([47a4789](https://github.com/this-is-tobi/rta/commit/47a478920f5c728ff4f6e8a910cd900100837fff))
* **pluginhost:** an installed plugin can read its own directory, so it can verify a certificate ([043051c](https://github.com/this-is-tobi/rta/commit/043051c8c2b114748d986eda5359414f744fb173))
* **profile:** a picked instance seeds the form it was picked on ([ad2a430](https://github.com/this-is-tobi/rta/commit/ad2a4303dcfb2bfe63899046c463e88d6e0c7ecb))

## [0.13.0](https://github.com/this-is-tobi/rta/compare/v0.12.0...v0.13.0) (2026-09-06)


### Features

* **agent:** the log filters by role, the overview names the roles standing, and allow takes --role ([16a240d](https://github.com/this-is-tobi/rta/commit/16a240d301ab97dd8d5174ea630f178a269bda01))
* **audit:** the harness deny list is derived from the catalogue ([59c6060](https://github.com/this-is-tobi/rta/commit/59c60606234de562edb888ad7b6e92751e0d962d))
* **config:** a role is a named list of grant lines, in your config or the team's policy file ([42fb56e](https://github.com/this-is-tobi/rta/commit/42fb56e836ef54b006f7340c913acdbc584f3a79))
* **grant:** a grant remembers its role, and Issue says what it replaced ([411d7b3](https://github.com/this-is-tobi/rta/commit/411d7b3571557a065d4348e1f4e3956ce260babc))
* **grant:** a role is issued whole under one passphrase — grant issue, grant roles, --role ([ae9e6dd](https://github.com/this-is-tobi/rta/commit/ae9e6dde34f17a15fb6f6ffbdddff0e315ff4af5))
* **grant:** roles in force on the roster, and an own role may name its agent ([b3ba407](https://github.com/this-is-tobi/rta/commit/b3ba407b2ebdcf7afc0b7b00dbdbfc42712e850a))
* **record:** a row names the role the covering grant was issued under ([cf8f4c7](https://github.com/this-is-tobi/rta/commit/cf8f4c7da7cfdc5bdb03a86957b377fb25f709e1))


### Bug Fixes

* **policy:** a key the file does not have refuses the file ([5ad448c](https://github.com/this-is-tobi/rta/commit/5ad448cdb3f188555930351ce835ab931183326e))

## [0.12.0](https://github.com/this-is-tobi/rta/compare/v0.11.0...v0.12.0) (2026-09-06)


### Features

* **agent:** presence says whether a server asks, and where its paths are confined ([c92ba63](https://github.com/this-is-tobi/rta/commit/c92ba6376504f694ac4a2c0a9ff40fb832484609))
* **consent:** a retry of a parked call joins the same question ([92bc4bf](https://github.com/this-is-tobi/rta/commit/92bc4bff67dcaa1fd8e74fcaa320df10b3ba637e))
* **doctor:** standing locks are a row ([32cf950](https://github.com/this-is-tobi/rta/commit/32cf950c534cb67ad1951d879013fa4df971d1ae))
* **doctor:** the data directory's mode is a row ([9716083](https://github.com/this-is-tobi/rta/commit/97160836e05743f8dd6f1a74dac7a733d8d0b114))
* **mcp:** a caller that keeps being refused is answered slower, and so is a rejected bearer ([bbd1970](https://github.com/this-is-tobi/rta/commit/bbd1970c302a4730f9e6c80c204aaaef8c763caa))
* **plugin:** an input can say it points at another machine, or is only read beside one ([972d09b](https://github.com/this-is-tobi/rta/commit/972d09b904140e135e04d55fac226cc3a1787f56))
* **tui:** the lock key fills the agent in, the queue refreshes, and a row's form stays local ([b332fd6](https://github.com/this-is-tobi/rta/commit/b332fd66fe6e1e063ad466ec4ff41ea61b40c165))


### Bug Fixes

* **agent:** the detailed overview's empty sections are sentences ([0e8538c](https://github.com/this-is-tobi/rta/commit/0e8538cfe8dfa4a941b18e28fad635ac953d698a))
* **agent:** the record's section is addressed as record ([1f0507b](https://github.com/this-is-tobi/rta/commit/1f0507bb655c3c9a654a9e0a0b5e78df57817318))
* **app:** a missing argument is named, with the line to type ([994a59f](https://github.com/this-is-tobi/rta/commit/994a59f53206e9aa6143758e9e1476dd3ba18be9))
* **app:** long helps flow to the terminal's width ([deb2e62](https://github.com/this-is-tobi/rta/commit/deb2e62d8a5b3a33f7e0f9f520b6d56968dfa3fa))
* **explain:** a human-only card no longer prices a grant it would refuse ([18e0a0c](https://github.com/this-is-tobi/rta/commit/18e0a0ced0c01da42be0e0ebdf53c5b8e2e6a68d))
* **grant:** completion, the guard line and the allow sentence say only what is true ([dac1c52](https://github.com/this-is-tobi/rta/commit/dac1c523a713ca9744f8c9aba64ed846d2600a1a))
* **http:** a summary the help renderer keeps as written ([6bc7ab0](https://github.com/this-is-tobi/rta/commit/6bc7ab04747795add4810861a5eadcfcdb29e826))

## [0.11.0](https://github.com/this-is-tobi/rta/compare/v0.10.0...v0.11.0) (2026-09-06)


### Features

* **agent:** a connected client is visible before it makes a call ([16a6b34](https://github.com/this-is-tobi/rta/commit/16a6b3455e920e7b3e4ae88d7b99ca0a58c774d3))
* **agent:** the log ends on the latest call, and a table can say its newest row is last ([a084c67](https://github.com/this-is-tobi/rta/commit/a084c675d5be58c60fc518ef2eb572513af7a4ff))
* **grant:** a grant is the only allow, and it binds to the artifact ([5379c61](https://github.com/this-is-tobi/rta/commit/5379c61cd3a6efee5db19f5092455d4eb95b7cea))
* **mcp:** every agent has a name, and a grant knows whose it is ([3bdab34](https://github.com/this-is-tobi/rta/commit/3bdab34e5e5adeab79744f95e5ad6af0b1be16ef))
* **pkg:** which managers the machine has is a table that answers in a moment ([5029ea8](https://github.com/this-is-tobi/rta/commit/5029ea8265ef2026e4bc9117cb712868bf7063b5))
* **plugin:** a capability can say it is for the person at the terminal ([bf0eff4](https://github.com/this-is-tobi/rta/commit/bf0eff4ffb9ee404690771b93f59eb5e6ba71170))
* **tui:** the arrows browse a box's offer before anything is typed ([fde9e1a](https://github.com/this-is-tobi/rta/commit/fde9e1afdfba80f4e6beb43e843aff6fc6cd732c))


### Bug Fixes

* **agent:** a live answer with --ttl names the record it granted ([38172be](https://github.com/this-is-tobi/rta/commit/38172be11ee2abc07b85b3fd0c108ea80543da40))
* **agentlog:** a lost end mark is admitted in the chain, and a key is never minted over a record ([66a8930](https://github.com/this-is-tobi/rta/commit/66a89307d571b8afd24789fd51cf31be7177f522))
* **agentlog:** one oversized row can no longer end the record ([6a86217](https://github.com/this-is-tobi/rta/commit/6a86217c27ad3a90fe94f8a50a860730a55ed8e8))
* **agent:** the queue and the overview say what stands, and an answer says when it releases nothing ([d10d23a](https://github.com/this-is-tobi/rta/commit/d10d23a70758c3357330b2e848455327d28bbd6c))
* **agent:** the record ships from its cursor forward, counts by time, and names unknown tools ([3be9c68](https://github.com/this-is-tobi/rta/commit/3be9c68bf42cd6de55540ddbd89b693c4af18adf))
* **app:** a noun's help names its verbs, and no help line opens with a word the renderer mangles ([278d5ab](https://github.com/this-is-tobi/rta/commit/278d5abc4f0c4071409fb3dc19c42b4374fbd11b))
* **grant:** a grant nothing could spend is refused, and the rest is said in a person's words ([42a9abb](https://github.com/this-is-tobi/rta/commit/42a9abbd541cd2816bade2643e65560bbd95433f))
* **grant:** one write for the guard, one screen for its state, renew like revoke, rows seed ([0571fcf](https://github.com/this-is-tobi/rta/commit/0571fcf093c72fd83dbc69e00530924d648f070c))
* **lockdown:** a locked agent is not handed its own unlock command ([5977101](https://github.com/this-is-tobi/rta/commit/5977101837b6e0cbcdedfdd41106689d89520cc4))
* **lock:** who placed a lock is measured, not assumed ([b075221](https://github.com/this-is-tobi/rta/commit/b075221b54e875b2b75f2e86225635fcab515d34))
* **mcp:** a failed call spends its use, a declined call says so, and the hint names the agent ([6b6cb0b](https://github.com/this-is-tobi/rta/commit/6b6cb0b50d1e5b150ae072b3ff9319c5bbd50786))
* **mcp:** a machine that requires a repository policy refuses to serve without one ([4d7f04e](https://github.com/this-is-tobi/rta/commit/4d7f04e76aaf7c16cae941819dbcac640edd6ba8))
* **mcp:** a short token is refused, an exposed bind is announced, and startup says what it means ([158aa48](https://github.com/this-is-tobi/rta/commit/158aa48c6ccac1b93ea857a7e8ca4d7d39991fd6))
* the data directory is created for its owner alone ([aada406](https://github.com/this-is-tobi/rta/commit/aada406ee049d4fdadf092a6c88ee91898ccdaad))


### Code Refactoring

* **agentlog:** the record says which files are its own ([0fd5d8c](https://github.com/this-is-tobi/rta/commit/0fd5d8ceb67946782bf506b29c0e245d11d9d00a))
* **session:** an open server says so, instead of being guessed at ([460f1cb](https://github.com/this-is-tobi/rta/commit/460f1cb8df5bba51a37ae42938cea47f20765198))
* trim the surfaces the deep check found saying too much ([58a6947](https://github.com/this-is-tobi/rta/commit/58a69475706d4382ffd3feb869693e833db4d634))

## [0.10.0](https://github.com/this-is-tobi/rta/compare/v0.9.0...v0.10.0) (2026-09-05)


### Features

* **http:** a status code explains itself, offline ([df1ca22](https://github.com/this-is-tobi/rta/commit/df1ca22dde9f74f4828656355ed14ded0841a894))
* **pkg:** what is outdated on this machine, and one upgrade at a time ([072df88](https://github.com/this-is-tobi/rta/commit/072df8861ef76ec96706fbd0ca8a07b6f9bceec0))
* **profile:** the badge colour is offered where a profile is edited ([84202d7](https://github.com/this-is-tobi/rta/commit/84202d792bbd07c48a1f79a0fda4f5e72fe14670))
* **tui:** the forward's coordinate is in the boxes, and typing over it goes around the forward ([474ee80](https://github.com/this-is-tobi/rta/commit/474ee80b5318e01eec988947d47e9ba18fe91a0a))
* **tui:** the instant no is one key away from the screen that makes you want it ([b76b012](https://github.com/this-is-tobi/rta/commit/b76b012d6bb29cd7b29f1040786b1b2e3ea7e7de))

## [0.9.0](https://github.com/this-is-tobi/rta/compare/v0.8.0...v0.9.0) (2026-09-05)


### Features

* **git:** a branch says what it tracks and whether that still exists ([c9a1ca3](https://github.com/this-is-tobi/rta/commit/c9a1ca3334bed7ed13e25b812e783c552e481a5e))
* **plugin:** a plugin's reference page is written from its declaration ([51125d5](https://github.com/this-is-tobi/rta/commit/51125d5c584a72d5187b8dae63ea0ea4dfdb3e39))


### Code Refactoring

* **note:** a to-do is a note with a checkbox, and one key flips it ([b97fe77](https://github.com/this-is-tobi/rta/commit/b97fe772ca1323ab2565ec5218abd4010e0d5840))

## [0.8.0](https://github.com/this-is-tobi/rta/compare/v0.7.0...v0.8.0) (2026-09-05)


### Features

* **audit:** the namespace an audit narrows to is a flag a profile can carry ([a21f32f](https://github.com/this-is-tobi/rta/commit/a21f32fd33458984b0b2602f37fec1d1c029f7b5))
* every preference an operator would set once is a config key ([0ac3ff6](https://github.com/this-is-tobi/rta/commit/0ac3ff6fa15ac1466075a0deced21f1d55314495))
* **kv:** a mistake is undone by name, and the store has a shape ([36a139d](https://github.com/this-is-tobi/rta/commit/36a139dd4d39757fbbeab0f0394c5bd34972e9cc))


### Code Refactoring

* the repository is rta, the name the binary has always had ([cf9a973](https://github.com/this-is-tobi/rta/commit/cf9a9731ccd4130a0c91cc37b70a1e7b2598e0db))

## [0.7.0](https://github.com/this-is-tobi/rule-them-all/compare/v0.6.0...v0.7.0) (2026-09-04)


### Features

* **eol:** a watchlist graded in one call, and a search over the catalogue ([ac87fbc](https://github.com/this-is-tobi/rule-them-all/commit/ac87fbcd3a8cbacbd8143177cf5a621bdfa526dd))
* **eol:** end-of-life checks are built in ([4a58644](https://github.com/this-is-tobi/rule-them-all/commit/4a586449d58712d75d9a981eab31e5c15995b7f6))
* **plugins:** an index keeps its manifests under index/, not plugins/ ([27619b4](https://github.com/this-is-tobi/rule-them-all/commit/27619b406820fd60994091c9fc63b383d5d2de2b))
* **plugins:** the first-party index is known by name ([f5ec863](https://github.com/this-is-tobi/rule-them-all/commit/f5ec863774ee172e0332f8081d5a8bd54814aaea))


### Code Refactoring

* **plugins:** the eleven first-party plugins move to rta-plugins ([fbd2950](https://github.com/this-is-tobi/rule-them-all/commit/fbd2950d997e6936c1474348a20734fbe42b07ea))


### Dependencies

* **docker:** the full image installs each plugin's own release ([498b176](https://github.com/this-is-tobi/rule-them-all/commit/498b176ceceb6707753cd7fb5442cdc486820283))

## [0.6.0](https://github.com/this-is-tobi/rule-them-all/compare/v0.5.0...v0.6.0) (2026-09-04)


### Features

* **plugins:** a plugin's version is stamped by the build, not typed into it ([01479b1](https://github.com/this-is-tobi/rule-them-all/commit/01479b1bcac1b51730d68cf74454a567d1065247))
* **plugins:** install a plugin from an OCI registry ([de0b30f](https://github.com/this-is-tobi/rule-them-all/commit/de0b30f0de848e3e3fb1fe8f1e628761244f40ae))


### Bug Fixes

* **plugins:** a file:// artifact could not be installed on Windows ([7a5c666](https://github.com/this-is-tobi/rule-them-all/commit/7a5c666c3a4a7cedbcfbf33290364c5565d53007))
* **plugins:** an install could fail on Windows because it had just run the artifact ([2aa6bfd](https://github.com/this-is-tobi/rule-them-all/commit/2aa6bfd300a29393fcbde79ca77005f5af69fa4b))
* **plugins:** no plugin could launch on Windows, whatever else was fixed ([90fcbd6](https://github.com/this-is-tobi/rule-them-all/commit/90fcbd6b433fa95d354d4fbd16e657001391a225))
* **plugins:** rta could not load a plugin on Windows at all ([779c126](https://github.com/this-is-tobi/rule-them-all/commit/779c126a2c21b2c1c92c099bb6f7ea7c3e8fd9d3))
* **tui:** fixing a stale pin dropped every key the form did not show ([8a48e2a](https://github.com/this-is-tobi/rule-them-all/commit/8a48e2ac900983a46b39365b31afb7ddab92b5ea))
* **tui:** re-pinning or labelling a connection erased what it configured ([b9e7ad7](https://github.com/this-is-tobi/rule-them-all/commit/b9e7ad73e1b18a4399522f594276409597e4a5b0))

## [0.5.0](https://github.com/this-is-tobi/rule-them-all/compare/v0.4.0...v0.5.0) (2026-09-04)


### Features

* **cnpg:** read the Backup objects, and ask for one using the cluster's own configuration ([2698c90](https://github.com/this-is-tobi/rule-them-all/commit/2698c902c943554060906579776f69bb0d998ce4))
* **cnpg:** recovery settings on the status page, and the volumes a cluster actually got ([8a8c0ad](https://github.com/this-is-tobi/rule-them-all/commit/8a8c0ad96df34e510d7717c9ec931e12e3e31928))
* **etcd:** back up the whole keyspace, and say why nothing restores it ([bb8508f](https://github.com/this-is-tobi/rule-them-all/commit/bb8508f7053487a61299359cb16f13d99bcdd910))
* **net:** net.listen — what this machine has open, and which process holds it ([ce82155](https://github.com/this-is-tobi/rule-them-all/commit/ce82155f804e7e6990fe5045661f14974c06c55c))
* **pg,mysql,mariadb,qdrant,vault:** say on the receipt what each backup leaves behind ([2ddd72e](https://github.com/this-is-tobi/rule-them-all/commit/2ddd72ece2fb9c6dfb51bf3568e933ef63af37e6))
* **profile,cli,tui:** mark an environment with a colour, and say which one you are in ([12a8f30](https://github.com/this-is-tobi/rule-them-all/commit/12a8f30d30df77b7fb4ba7855d99e24803a8527e))
* **time,codec,agent:** read an instant, and date the claims a JWT states as numbers ([c3b0165](https://github.com/this-is-tobi/rule-them-all/commit/c3b0165a12ab8460f92adafd9482557595abb349))
* **tui:** band the plugin inventory by where its bytes came from ([32ac5ee](https://github.com/this-is-tobi/rule-them-all/commit/32ac5ee964f3203a9636bd52e0d943074297e05f))
* **tui:** say which stored entry an environment fills a box from ([a6f5b9f](https://github.com/this-is-tobi/rule-them-all/commit/a6f5b9f9604feb4e50c2e00fa0ba694061986d5f))
* **view,cli,kube,sys:** grade a usage percentage green, amber or red ([f69c3c4](https://github.com/this-is-tobi/rule-them-all/commit/f69c3c467f5619de094419adac7f085371a4903b))


### Bug Fixes

* **cnpg:** the backup pre-flight was blind to the arrangement it was written for ([c6967f5](https://github.com/this-is-tobi/rule-them-all/commit/c6967f5aba41dd78563d01e53b22cd73b00b18f1))
* **plugin:** a repository that is not an index is refused, not attached empty ([5ab0259](https://github.com/this-is-tobi/rule-them-all/commit/5ab025948a749893a81c486f9a245faa258b3ffb))
* **plugin:** an index is somebody else's repository, and Manifests read it like it was not ([279c30b](https://github.com/this-is-tobi/rule-them-all/commit/279c30b8d3599410ac7f6e5d3c46a87d81536870))
* **plugin:** search named one unreadable index and stopped ([93f608a](https://github.com/this-is-tobi/rule-them-all/commit/93f608a952777fabf28951a61ec1a9d322548f96))
* **profile:** two profile names could derive the same credential variable ([e0c776e](https://github.com/this-is-tobi/rule-them-all/commit/e0c776e2e2fd1381d81e0e74b2e826fdda0c3d34))
* **theme:** a usage cell rta could not read was painted green ([68034ce](https://github.com/this-is-tobi/rule-them-all/commit/68034ce7c6361b465a2184de4b2ebde76ed99b15))
* **tui:** the plugin inventory said "1 capabilities" ([634253b](https://github.com/this-is-tobi/rule-them-all/commit/634253bed2434b5d2ca7d9c2a53ec215b5ad9a75))

## [0.4.0](https://github.com/this-is-tobi/rule-them-all/compare/v0.3.0...v0.4.0) (2026-09-03)


### Features

* **agentlog:** split the code from the sentence - stable event codes on every row ([da55cb2](https://github.com/this-is-tobi/rule-them-all/commit/da55cb2b86cf0d8e7fe7fa0806e47f90563cdcc9))
* **kube:** provision grants logs, workloads, services and rollout - and the input completes ([5f9a64f](https://github.com/this-is-tobi/rule-them-all/commit/5f9a64f600819a07646f362d1f1ff09e8d646ab2))
* **mysql,mariadb:** dump and restore a database, for a person at a terminal ([3eb384a](https://github.com/this-is-tobi/rule-them-all/commit/3eb384a9518557dc6e34d70f86416dcbef6a157a))
* **operator:** roster rows take expires= - a departure date the clock enforces ([e1040ae](https://github.com/this-is-tobi/rule-them-all/commit/e1040ae2087c6f991d0e1c4ca45bc3750c9fd32b))
* **qdrant:** dump and restore a collection as a snapshot file, for a person at a terminal ([67a68d4](https://github.com/this-is-tobi/rule-them-all/commit/67a68d45df31c8c6b5f15e2ad117a3c7612c1b8e))
* **s3:** upload a directory into a bucket - bucket.download's other half ([33671f8](https://github.com/this-is-tobi/rule-them-all/commit/33671f8994f5aa54425fc99723907247332ae169))
* **vault:** restore a snapshot into a Vault, for a person at a terminal ([b538929](https://github.com/this-is-tobi/rule-them-all/commit/b538929dec0178755381afb2c00b1551b130513e))


### Bug Fixes

* **agentlog,operator,grant:** close the branch review findings ([3f86d61](https://github.com/this-is-tobi/rule-them-all/commit/3f86d61801b257d0b5d05e074d582e0c2eab9c0f))
* **image:** rta-full carries a MariaDB client, so its dump and restore run ([e994d97](https://github.com/this-is-tobi/rule-them-all/commit/e994d97f496bce4fad4949d158a749e29747100b))
* **mcp,plugin:** ledger a handler's policy gate as refused, not failed ([87d148d](https://github.com/this-is-tobi/rule-them-all/commit/87d148d6304e9086895e6025fc30128277e40090))

## [0.3.0](https://github.com/this-is-tobi/rule-them-all/compare/v0.2.0...v0.3.0) (2026-09-02)


### Features

* **agent:** pending, show, allow and deny gain --server - consent answered where you are ([3e54ccf](https://github.com/this-is-tobi/rule-them-all/commit/3e54ccf25fe9851d769e89f0550393160980112f))
* **consent:** digest-bound answers - DecideBound pins what was read ([256e996](https://github.com/this-is-tobi/rule-them-all/commit/256e996d58a2d18e196d0a216099e897e616535b))
* **grant:** allow and revoke gain --server - grants managed where you stand ([32a01ad](https://github.com/this-is-tobi/rule-them-all/commit/32a01adf63dc0bb179bbc433bdbbbfa33a823aab))
* **grant:** grant guard on/off/status - the passphrase gate's operator surface ([62a2f7f](https://github.com/this-is-tobi/rule-them-all/commit/62a2f7f4c48d232d2ec97dd67120d69f0584b481))
* **grant:** guard signatures over grant authority, enforced on read and issue ([19e80c7](https://github.com/this-is-tobi/rule-them-all/commit/19e80c7cd6d03c0bb435dd2cc0aa116c6b1a25dc))
* **grant:** rta grant guard remote - enroll a roster as this machine's guard ([085adc2](https://github.com/this-is-tobi/rule-them-all/commit/085adc2247171b5ce394078bf3ef180cc071316c))
* **grant:** the guard gates allow, renew and agent allow --ttl ([d610461](https://github.com/this-is-tobi/rule-them-all/commit/d6104617d7c81696d18252cae8dc8e51f4ef2082))
* **guard:** operator passphrase gate - key wrapping and signatures ([96f8993](https://github.com/this-is-tobi/rule-them-all/commit/96f89935655dd99ffa47415ead004918699e7f4c))
* **guard:** remote mode - a guard whose keys live with operators elsewhere ([eed8208](https://github.com/this-is-tobi/rule-them-all/commit/eed820854fe4286879d5ac3c758aa3304f8bedec))
* **lock:** freeze one principal now - the instant path revocation was missing ([c9c9bd0](https://github.com/this-is-tobi/rule-them-all/commit/c9c9bd0a722cebebcc2c38736e20a59bba4400ec))
* **mcp:** --consent starts beside --http when --operators names who answers ([1626535](https://github.com/this-is-tobi/rule-them-all/commit/162653575b4f54d3ebee4bf39148155465da2d1f))
* **mcp:** mount the operator channel beside the MCP endpoint ([665378e](https://github.com/this-is-tobi/rule-them-all/commit/665378efcaafad1fb7e5308fdac04591ebc7bfe7))
* **mcp:** the operator channel's consent verbs - list the queue, answer a call ([d79d1a8](https://github.com/this-is-tobi/rule-them-all/commit/d79d1a8890a1a3f23f6259721fafcb74726b1611))
* **mcp:** the operator channel's mutation verbs - revoke, prepare, issue ([0965912](https://github.com/this-is-tobi/rule-them-all/commit/0965912dd26c82c70d25556c41c681c4ea435041))
* **operator,agentlog:** ledger the channel's mutations, attributed to the signing key ([fe170bc](https://github.com/this-is-tobi/rule-them-all/commit/fe170bce89cbd8a23fa6baf1c4864449fae89832))
* **operator:** role=read roster rows - enrollment that watches but cannot act ([16dfa11](https://github.com/this-is-tobi/rule-them-all/commit/16dfa1152203880ae0aca958b5274e895ebbb3a1))
* **operator:** the client side - one CLI, many servers, HITL on every call ([a309f1e](https://github.com/this-is-tobi/rule-them-all/commit/a309f1e52e2132dd178359f398bc431be47ccd20))
* **operator:** the identity layer of the remote operator channel ([bd3d558](https://github.com/this-is-tobi/rule-them-all/commit/bd3d55897fcf14e198a5cf590433745b90baa8f7))
* **tui:** bare actions - the consent pane keeps its one-key answers ([b20825c](https://github.com/this-is-tobi/rule-them-all/commit/b20825c975cde9e881dc539043fbda4ea30467f7))


### Bug Fixes

* **consent,tui:** close the stage-3 review's deferred lows - orphan sweep, bare soundness ([5b57d9c](https://github.com/this-is-tobi/rule-them-all/commit/5b57d9c2cc4701d750fe4c58cea1c676f778dded))
* **deps:** upgrade x/crypto to v0.56.0 for the ssh channel DoS advisories ([9d29e90](https://github.com/this-is-tobi/rule-them-all/commit/9d29e908a43f7394e8b7d203b17b3ca1361e8140))
* **guard,mcp:** close the roster review's lows - drift warning, honest statuses ([3aa761c](https://github.com/this-is-tobi/rule-them-all/commit/3aa761c9696c8626771fcd5ffd7d9b0da259e56c))
* **guard:** close the review findings - argv channel, silent rollback, races ([14449e0](https://github.com/this-is-tobi/rule-them-all/commit/14449e082582aa466d7d8613ceaaa812982846f7))
* **guard:** refuse a remote guard before the prompt, not after the typing ([d997519](https://github.com/this-is-tobi/rule-them-all/commit/d997519d62734e2037840b6ab11cafe5dd7852fb))
* **lockdown,mcp:** close the lock review findings - credential grammar, parked race, recovery hint ([436f1fd](https://github.com/this-is-tobi/rule-them-all/commit/436f1fdec3c3331dcc15ccaa2277d467768c53a0))
* **mcp:** classify the wired packages' refusals - the ledger and the pager were both wrong ([c246b3a](https://github.com/this-is-tobi/rule-them-all/commit/c246b3a55c5ece8e0309c7fe598a75d044ca1545))
* **operator:** close the security review's findings - above all, bind the server ([20ce35c](https://github.com/this-is-tobi/rule-them-all/commit/20ce35c1e29e44feaf6d0d819ffbb238965008f6))
* **operator:** close the stage-2 review - bind grants to their server, verify before signing ([b278c3b](https://github.com/this-is-tobi/rule-them-all/commit/b278c3b3ce21ae2590d44f4bb012d5b9ceb22a45))
* **operator:** consent verbs answer only for the server that parks ([8b48dcd](https://github.com/this-is-tobi/rule-them-all/commit/8b48dcd47c0244a640100118b0d3eac7f5259359))
* **operator:** one spelling per key - strict base64, dedup by decoded bytes ([b23259d](https://github.com/this-is-tobi/rule-them-all/commit/b23259d3cd4c5efd2ac8e580614307240fc93671))


### Code Refactoring

* **guard:** extract the passphrase-wrapped-key shape into internal/passkey ([9cc9e65](https://github.com/this-is-tobi/rule-them-all/commit/9cc9e65470f972e63ebfe19f6e04ffbe0a43958f))

## [0.2.0](https://github.com/this-is-tobi/rule-them-all/compare/v0.1.0...v0.2.0) (2026-09-02)


### Features

* **agent:** repair a record that lost only its integrity mark ([76d2ef0](https://github.com/this-is-tobi/rule-them-all/commit/76d2ef081a5bb6b1bba3620ad26e85f3e6547ecc))
* **app:** state, remove and complete profile instances from the CLI ([5de2380](https://github.com/this-is-tobi/rule-them-all/commit/5de2380fb75ccf5495a3d0542ee03adf23cf304e))
* **audit:** audit.agents --fix prints the exact edit for each finding ([9cdc272](https://github.com/this-is-tobi/rule-them-all/commit/9cdc272e0f8ce017a4c3e459038a78ccae05e010))
* **config:** instance labels in profile plugin keys ([9274d96](https://github.com/this-is-tobi/rule-them-all/commit/9274d963f4f20a93373eeed80ce63c689321b421))
* **config:** print the file's JSON Schema for editor completion ([859590c](https://github.com/this-is-tobi/rule-them-all/commit/859590cc378b0203151e153a1d89e063e98a1ff9))
* **docker:** a batteries-included image beside the narrow one ([9ac06ba](https://github.com/this-is-tobi/rule-them-all/commit/9ac06baa44eebf355bb5517d2007fb50e0e34ae3))
* **grant:** per-instance consent ([732e308](https://github.com/this-is-tobi/rule-them-all/commit/732e30857b7ba05e3f5589b9118a6320223f2ecf))
* **kv:** say which profiles use each entry, off the agent surface ([b3ac28b](https://github.com/this-is-tobi/rule-them-all/commit/b3ac28b4cdc3d0459060696168b375589eed1a53))
* **profile:** read a credential from a cluster without forcing a forward ([a2130dc](https://github.com/this-is-tobi/rule-them-all/commit/a2130dc8722799ae8049b1d4ea20ffdb4d4501a6))
* **profile:** resolve instance refs, stamp per instance ([62aec35](https://github.com/this-is-tobi/rule-them-all/commit/62aec357b4ffa4f196764761f19499ca8a559daa))
* **tui:** arm profile deletes behind a second keypress ([645b517](https://github.com/this-is-tobi/rule-them-all/commit/645b517d471e6c60e7ad8fa89a70d3a255e51208))
* **tui:** edit, pick and read profile instances ([ed2babb](https://github.com/this-is-tobi/rule-them-all/commit/ed2babb8c5710d18ee2307f02a6878fde965d9fb))


### Bug Fixes

* **agent:** measure a parked answer's origin instead of assuming a person ([0a9d29b](https://github.com/this-is-tobi/rule-them-all/commit/0a9d29b799f0ce4bfac8ecee9e18c7f9d2e60687))
* **config:** explain a repeated plugin key instead of advising rta init ([fa7c5af](https://github.com/this-is-tobi/rule-them-all/commit/fa7c5af15ea596927758be970c049ce926ba97f6))
* **docs:** stop the boundary diagram clipping its own node text ([c4ccd44](https://github.com/this-is-tobi/rule-them-all/commit/c4ccd44dd08a942736a5d46109a2c3ea0cc69f11))
* **kv:** relabel an entry without re-supplying its secret ([43af6fa](https://github.com/this-is-tobi/rule-them-all/commit/43af6faa82e208aabc80a2486f16878041335024))

## 0.1.0 (2026-09-01)


### Features

* agent consent and policy infrastructure ([8ddf46e](https://github.com/this-is-tobi/rule-them-all/commit/8ddf46ebe1adbc13dd99b9a05ab600259166533a))
* **audit:** add kube.* compliance checks - RBAC, pod security, quotas, netpol ([ea4c647](https://github.com/this-is-tobi/rule-them-all/commit/ea4c6472c6a70ff1a9991cfe4247b8bed9b9592d))
* **audit:** narrow the kube.* audits to one namespace, honestly ([d448b63](https://github.com/this-is-tobi/rule-them-all/commit/d448b6364b34c181d44d221a658e5ad0d4c124f8))
* **cnpg:** complete contexts, namespaces and cluster names ([7752761](https://github.com/this-is-tobi/rule-them-all/commit/7752761c5c239641bf030685e398ea3cf7acd924))
* **cnpg:** deepen cnpg.status to what the resource actually says ([96bc7d8](https://github.com/this-is-tobi/rule-them-all/commit/96bc7d8f3387fb969e12e0f112df8d50307d0277))
* **kube:** add kube.event.list, keyed on how long a problem has run ([018da6c](https://github.com/this-is-tobi/rule-them-all/commit/018da6cd0c71c5a94a29f9394fe1355ffa8c7595))
* **kube:** add kube.metrics.pressure and kube.pvc.usage ([986a6db](https://github.com/this-is-tobi/rule-them-all/commit/986a6db600916e384900c8ceea161408a35db132))
* **kube:** add SRE diagnostics - quotas, PVCs, metrics, cert expiry ([3bcd3d1](https://github.com/this-is-tobi/rule-them-all/commit/3bcd3d129837636c20d9101cb3938fafd3218087))
* **kube:** mint a scoped ServiceAccount identity instead of the operator's own ([e3849e3](https://github.com/this-is-tobi/rule-them-all/commit/e3849e3451fb3d78f92bfb94308c289beff946e7))
* **kube:** report node health in the overview, and add kube.node.list ([c01de18](https://github.com/this-is-tobi/rule-them-all/commit/c01de18c5d9761426ca25af3e83d4a87d59a8de4))
* **mcp:** a locality gate for capabilities that describe this machine ([352ea77](https://github.com/this-is-tobi/rule-them-all/commit/352ea775fc68568ce3ea112de6e37ef53cc2d725))
* **mcp:** add OIDC as a second bearer-auth mechanism for --http ([89fc7da](https://github.com/this-is-tobi/rule-them-all/commit/89fc7dab756711af8022af6b64d4d32bae1b2b7f))
* **mcp:** serve over HTTP, bearer-authenticated, fail-closed ([45969fd](https://github.com/this-is-tobi/rule-them-all/commit/45969fd5b1e68193b30e42b0a5a7a500d54bba57))
* **pg:** restore what pg.dump writes, closing the backup chain ([2628b08](https://github.com/this-is-tobi/rule-them-all/commit/2628b0874effe3930f169b99919c1c1dc29e491b))
* **plugin:** add outdated, a cheap label check across installed plugins ([efdad2a](https://github.com/this-is-tobi/rule-them-all/commit/efdad2accf4228fb1fa00aa4bdeffbb2ceef1c97))
* **plugin:** add SecretSlice, so a repeatable credential can be declared ([913146a](https://github.com/this-is-tobi/rule-them-all/commit/913146aa4166538f9a9eaa342074e9e328c53338))
* **profile,plugin:** trust TLS across tunneled and direct connections ([817a021](https://github.com/this-is-tobi/rule-them-all/commit/817a021723477ba64faeeb86f7d7e445669479c4))
* profiles as environments, tunnels, plugin trust, and distribution begins ([2bf9f82](https://github.com/this-is-tobi/rule-them-all/commit/2bf9f826f7dbdfe23fc10da0956370ee51aae664))
* **release:** publish a Docker image and attest release binaries ([ad7e873](https://github.com/this-is-tobi/rule-them-all/commit/ad7e8730bc844bcc3ad8a8acb0f2e5246c0d906d))
* the capability model, three surfaces, and the first built-ins ([5ee1ca0](https://github.com/this-is-tobi/rule-them-all/commit/5ee1ca013314160928f4e1391bf507b4a526806e))
* the plugin catalogue grows, remote audit, release packaging, and observability ([ce2a50b](https://github.com/this-is-tobi/rule-them-all/commit/ce2a50bde4659dea1f9e8213b938a0175ee064ea))
* the plugin SDK and host, confinement hardening, and the first plugins ([92c3425](https://github.com/this-is-tobi/rule-them-all/commit/92c342552b5393edf820af89ee0710de51e06b94))
* **tui:** allow a plugin its credential locations from the pane that shows them ([043835e](https://github.com/this-is-tobi/rule-them-all/commit/043835e797016be68d89e0e4a026fb0a4f6fa93e))


### Bug Fixes

* **agent:** agent.allow now enforces the team's never/neverProfile ceiling ([0a9565e](https://github.com/this-is-tobi/rule-them-all/commit/0a9565ea66efb2cf05262b8c8a8e23044809e6eb))
* **app:** plugin allow no longer drops earlier grants it doesn't re-list ([18d1079](https://github.com/this-is-tobi/rule-them-all/commit/18d10791764c5aba9310287484c7fd1212b262da))
* **atomicfile,consent,grant,seal,agentlog,profile,plugintrust:** bound every read of rta's own state ([fca1e33](https://github.com/this-is-tobi/rule-them-all/commit/fca1e33c2ffdff53ec827f11603c431b8eaacc76))
* **audit:** a requirements.txt pin with no version no longer panics ([a8aadcc](https://github.com/this-is-tobi/rule-them-all/commit/a8aadccd7dd7962f983c011aa148d5b0394459f2))
* **audit:** scoped npm packages no longer parse to a name OSV can't match ([5dc82bf](https://github.com/this-is-tobi/rule-them-all/commit/5dc82bf8c2e5f4c8a6a06ea6e3732852aba7d45f))
* **build:** refuse a plugin directory name the shell would expand ([f5259e7](https://github.com/this-is-tobi/rule-them-all/commit/f5259e77eb17c75f2322ba18cd1c7b06b3c3d1ee))
* **cert:** require a grant before cert.expiry dials caller-chosen hosts ([6540dc0](https://github.com/this-is-tobi/rule-them-all/commit/6540dc004b8436717a31dc998447a4ef2cbea616))
* **clipboard:** bound the clipboard-program shell-out and reap what it forked ([01e40e5](https://github.com/this-is-tobi/rule-them-all/commit/01e40e57fa65221cb00dd38ed37a6689226e5889))
* **cnpg:** refuse --namespace and --all-namespaces together ([d736016](https://github.com/this-is-tobi/rule-them-all/commit/d736016994cc0a7e0d984ad324539e48a95fc976))
* **config,profile,pluginconf:** a namespace named twice is ambiguous, not first-match-wins ([2a2fc06](https://github.com/this-is-tobi/rule-them-all/commit/2a2fc06766eb64e81056cdc4e26310b5b93deba5))
* **config:** stop honouring plugins: and dashboard: from a file nobody named ([59f1d8b](https://github.com/this-is-tobi/rule-them-all/commit/59f1d8bcdaff3cb201363d1c6a0199336272ae31))
* **consent,agentlog,seal:** close the gaps in the record's own trust machinery ([1fe573d](https://github.com/this-is-tobi/rule-them-all/commit/1fe573d5f4c6684cf49522d1b3e95cb213b36524))
* **filelock:** close breakStale's renewal race and its restore gap ([9964e93](https://github.com/this-is-tobi/rule-them-all/commit/9964e93dd2bd423ce4a74a9364e4e45f49185b92))
* **gitclone:** cap how much object data one InMemory clone may store ([fb28ae9](https://github.com/this-is-tobi/rule-them-all/commit/fb28ae98aea3bd7c6586b7e4154244be47191fd4))
* **grant,agent:** apply the team's TTL ceiling everywhere a grant is issued ([7ae2479](https://github.com/this-is-tobi/rule-them-all/commit/7ae247932938d581562cd02499bcdc420ebb5ff6))
* **grant:** a rate-exhausted grant no longer masks an unrelated missing one ([e6c4f52](https://github.com/this-is-tobi/rule-them-all/commit/e6c4f5244e4b73e49669733544d8812c03e021df))
* **grant:** grant.list no longer hands any MCP caller the whole roster ([26bb3b7](https://github.com/this-is-tobi/rule-them-all/commit/26bb3b7b022006ea7790adc054954e411ec8afda))
* **grant:** spending an unrelated grant no longer deletes a ceiling-suppressed one ([e0527c9](https://github.com/this-is-tobi/rule-them-all/commit/e0527c981bad4fcfabe219afaf60fcf7138d22ef))
* **http:** block SSRF-adjacent addresses and flag truncated bodies ([36224df](https://github.com/this-is-tobi/rule-them-all/commit/36224df5d26aed2f3447353a70d5b3fdaa9b72c3))
* **kube,cnpg,tui:** close the classes the first pass only closed instances of ([237a807](https://github.com/this-is-tobi/rule-them-all/commit/237a80732cf532e8f4ec8b3f4e931f67b3a6ede9))
* **kube:** correct cert.list's false claim that it never requests tls.key ([cecdf7c](https://github.com/this-is-tobi/rule-them-all/commit/cecdf7ce79254bb033e8217aae6507da38aa0152))
* **kube:** keep namespace completion working when both namespace flags are set ([237a807](https://github.com/this-is-tobi/rule-them-all/commit/237a80732cf532e8f4ec8b3f4e931f67b3a6ede9))
* **kube:** namespace completion contacts the cluster, so mark it Live ([4f14173](https://github.com/this-is-tobi/rule-them-all/commit/4f1417381526658c4923e8f22c202bef54114973))
* **kube:** refuse --namespace and --all-namespaces together ([72bd202](https://github.com/this-is-tobi/rule-them-all/commit/72bd202946d537eb2b3c162dd406d6c9447751a9))
* **kube:** reject a provision --ttl below Kubernetes' own 10m token floor ([4e6ea10](https://github.com/this-is-tobi/rule-them-all/commit/4e6ea106badcbd3b84b0ce9dea267a09d1b66e4a))
* **kube:** stop counting completed Jobs as unhealthy pods ([ec4cef1](https://github.com/this-is-tobi/rule-them-all/commit/ec4cef194e00c31519fb4e763b80de2d18092441))
* **kube:** stop reporting a cluster's refusal as a cluster's silence ([4b68b10](https://github.com/this-is-tobi/rule-them-all/commit/4b68b10e968a3557aafebbeb9661e1d662dfc882))
* **kube:** write the minted kubeconfig atomically rather than truncating ([e509c16](https://github.com/this-is-tobi/rule-them-all/commit/e509c16ba3aced5becd3dfbde71a61e2f15854cc))
* **kv:** garbage in kv.recipients no longer locks out the real passphrase ([048ce8a](https://github.com/this-is-tobi/rule-them-all/commit/048ce8a8f9856182736f23705d49cbd849398166))
* **mcp,agent:** audit.deps, audit.why and kv.status describe this machine too ([9b23250](https://github.com/this-is-tobi/rule-them-all/commit/9b23250b40fa3457ea372b5a2c31dfa71fb3a24f))
* **mcp,app:** refuse a group-writable token file, and stop overclaiming ([f12e48e](https://github.com/this-is-tobi/rule-them-all/commit/f12e48e06880f6f8456c6af0b1f729049143cf68))
* **mcp:** a digest pin below 8 hex chars no longer authorizes anything ([d0936ae](https://github.com/this-is-tobi/rule-them-all/commit/d0936aea3d6713d26398fb6d55787be89f66c1d4))
* **mcp:** close the auth, startup and shutdown gaps an adversarial review found ([0d4fafb](https://github.com/this-is-tobi/rule-them-all/commit/0d4fafb4f64096af621da4232a3187336186622b))
* **mcp:** recover a panicking capability instead of taking the whole server down ([81c40d4](https://github.com/this-is-tobi/rule-them-all/commit/81c40d433c10bbfe1a67130fb9ca840753d51995))
* **net:** require a grant before net.probe/net.port reach a caller-chosen host ([9cedcec](https://github.com/this-is-tobi/rule-them-all/commit/9cedcecc0e2536747a40aacbfa19ca2f01cafbfa))
* **pathguard:** refuse a UNC path before resolving it ([4bf8e75](https://github.com/this-is-tobi/rule-them-all/commit/4bf8e754c51ba1e80903990489a0b9cc7c42a728))
* **pg:** stop authenticating from the operator's ambient ~/.pgpass ([28e9d9d](https://github.com/this-is-tobi/rule-them-all/commit/28e9d9d89d712bb74c61f6e744233b47c5ef3260))
* **plugin,profile:** distribution hardening — credential needs, index manifests, profile repair ([580c34d](https://github.com/this-is-tobi/rule-them-all/commit/580c34d78c1c290f12b966a21622a375db4d572d))
* **plugin:** cap identifier length and one capability's input/option count ([034f26c](https://github.com/this-is-tobi/rule-them-all/commit/034f26ca876ff4c5e9f948b2f3bd440f1024d112))
* **plugindist:** validate what git may clone, and confine file:// to local indexes ([b83afc0](https://github.com/this-is-tobi/rule-them-all/commit/b83afc0723f9d6bc8c36be6109508a4c4b29da95))
* **plugintrust:** refuse an ambiguous digest prefix instead of taking all of it ([f3f3814](https://github.com/this-is-tobi/rule-them-all/commit/f3f38149a12c61b9a65e68a5f113789d5e57fd56))
* **policy,plugindist:** guard every YAML decode against alias expansion ([722248b](https://github.com/this-is-tobi/rule-them-all/commit/722248b3507494738563f4bd32232375e5ad67d3))
* **policy:** stop hand-duplicating plugin.Namespace, which had drifted ([6da3ead](https://github.com/this-is-tobi/rule-them-all/commit/6da3ead556dd9461e8d6f13926aaae12653de0c8))
* **render:** close a markdown injection gap and add csv formula-injection defense ([ca1b5c6](https://github.com/this-is-tobi/rule-them-all/commit/ca1b5c6848ea118c3fc697e366e290073a00ca94))
* **render:** escape the markdown fields that only look like identifiers ([74162e3](https://github.com/this-is-tobi/rule-them-all/commit/74162e3752fdf8ca50fdc28922e8837c95d78ffc))
* **s3:** bound copy, rename and rm to the record their grant names ([4933cf1](https://github.com/this-is-tobi/rule-them-all/commit/4933cf1db53ec977fa5d21dc417c93fce21995fb))
* **sys:** report the CPU model on arm64 and the disk in a container ([a3dae6b](https://github.com/this-is-tobi/rule-them-all/commit/a3dae6b6e80d31eecf8746466e5b9c193eb69dab))
* **todo,note:** concurrent writes to the same store no longer lose one ([dedb45f](https://github.com/this-is-tobi/rule-them-all/commit/dedb45f929a898368f1da026dade3c4d1bab27ae))
* **toolcall:** enforce Options on Int, Float and Bool fields too ([7e5df92](https://github.com/this-is-tobi/rule-them-all/commit/7e5df92b019941399d72926e7b66bda05a831907))
* **tui,app:** say the real reason a plugin has no dashboard tile ([9aaee51](https://github.com/this-is-tobi/rule-them-all/commit/9aaee51beb7cc827ff8d86aef35ad3c6daa19014))
* **tui:** "e" no longer shadows edit-inputs, and duplicate tiles no longer swap results ([4fbc57e](https://github.com/this-is-tobi/rule-them-all/commit/4fbc57e911e15fef0568a6abf8caf73874ed57a6))
* **tui:** edit-inputs no longer reseeds a Secret field with the last run's plaintext ([0acf386](https://github.com/this-is-tobi/rule-them-all/commit/0acf386e1163ca0bf62db1be328d1267ea7f1743))
* **tui:** let the search bar delete words and clear the line ([fc93ebf](https://github.com/this-is-tobi/rule-them-all/commit/fc93ebf0ab39d90dca11092ff9243e44f81d4985))
* **tunnel:** a splice that loses the closing race no longer leaks pipe fds ([cda3818](https://github.com/this-is-tobi/rule-them-all/commit/cda3818c26b955a657a5426214f3764111c70429))
* **tunnel:** refuse a kube coordinate segment that begins with a dash ([f3308b5](https://github.com/this-is-tobi/rule-them-all/commit/f3308b56cd5c6159cfc1c1609470f723fbbcb415))

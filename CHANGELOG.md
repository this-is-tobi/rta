# Changelog

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
* **plugin:** a fractional value for an integer input in plugins:, a profile or a tile's
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

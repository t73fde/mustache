# Mustache Template Engine for Go

This implementation will be integrated into
[Zettelstore](https://zettelstore.de).

It is a fork from [cbroglie/mustache](https://github.com/cbroglie/mustache),
which is itself a fork from
[hoisie/mustache](https://github.com/hoisie/mustache).

Basically, I needed a Mustache implementation for Go. But I did not need some
extra stuff, like reading templates from files or rendering a template without
an extra parse step or a command line tool. All I need is an implementation
without extra dependencies that will work with template code in memory only.

Therefore, I have removed all (hopefully) unneeded stuff. I re-licensed it
under the EUPL v1.2 or later, but included the original MIT license text.

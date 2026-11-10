# Carte de trame

## But et place
Noyau collaboratif : transformations opérationnelles (OT) sur arbres de nœuds et flux de texte, hub Go
(salles, pairs, sauvegarde) et session Dart qui garde un document aligné avec le hub, éditions hors ligne
comprises. Publié en tags git `vX.Y.Z` (Go et Dart dans le même dépôt).
Dépendants : **bref** et **fresque** (go.mod et `dart/pubspec.yaml`, ref `v0.5.0`), donc **server** et **app** par eux.

## Arborescence
- `.` : paquet Go `trame`, la moitié serveur (hub, salles, pairs, trames, formats).
- `ot/` : algorithmes OT Go (flux de texte `delta`/`doc`, arbres `tree`, clés d'ordre `key`).
- `trametest/` : aides de test pour les paquets qui bâtissent un hub (bref, fresque).
- `internal/charset/` : détection du jeu de caractères des fichiers lus.
- `testdata/ot/` : vecteurs communs, rejoués par Go et par Dart.
- `dart/lib/` : client Flutter ; `src/ot/` les mêmes OT, `src/session.dart` la session, `dart/lib/testing.dart` les faux pour les dépendants.
- `dart/test/` : tests Dart (arbre, delta, session).

## Où est X ?
- Session Dart (liaison, resynchro, hors ligne) : `dart/lib/src/session.dart`
- Hub, un document par salle : `hub.go`, `room.go`, un pair par connexion : `peer.go`
- Présence (curseurs, sélections) : `frames.go` (`presenceFrame`), `dart/lib/src/session.dart`
- Brouillon hors ligne : `DocDrafts` dans `dart/lib/src/session.dart`
- Format sur le fil : `frames.go` ; description : `README.md` § Protocol
- Formats de fichiers (lecture, sauvegarde) : `format.go`, `text.go`
- OT Go / Dart : `ot/delta.go`, `ot/tree.go`, `dart/lib/src/ot/delta.dart`, `dart/lib/src/ot/tree.dart`
- Vecteurs partagés Go/Dart : `testdata/ot`
- Versions : tags git `vX.Y.Z` ; `dart/pubspec.yaml` (`version:`)
- Changelog : `CHANGELOG.md` (Go) et `dart/CHANGELOG.md`
- Épinglage entre dépôts : aucun ici ; bref et fresque pointent sur trame (`go.mod`, `dart/pubspec.yaml` ref), app et server sur eux

## Construire et tester vite
Go (`GOWORK=off`, le go.work de la racine ne couvre pas tous les modules) :
- vet : `GOWORK=off go vet ./...` (1 s)
- un test : `GOWORK=off go test -run Vectors ./ot` (2 s, `ok`)
- bancs : `go test -run '^$' -bench Relay .`
Dart (flutter absent du PATH, via nix-shell ; `dart test` ne convient pas, `flutter_test`) :
- analyse : `cd dart && TMPDIR=/data/tmp nix-shell ../../app/shell.nix --run 'flutter analyze'` (~20 s)
- un fichier : `cd dart && TMPDIR=/data/tmp nix-shell ../../app/shell.nix --run 'flutter test test/delta_test.dart'`
  mesuré : 82 s au total (démarrage de nix-shell compris, 1 s de test), sortie `+5: All tests passed!`
- voie rapide (4 à 5 s au lieu de 22 s pour un fichier, mêmes sorties) : `TMPDIR=/data/tmp nix-shell ../../app/shell.nix --run 'export -p' > /data/tmp/app-env.sh` une fois, puis `cd dart && bash -c '. /data/tmp/app-env.sh; export TMPDIR=/data/tmp; flutter test -r failures-only test/<fichier>.dart'` ; détail dans app/docs/carte.md, « Construire et tester vite »
Pas de suite complète en boucle rapide.

## Publier
Lecture seule, rien n'est poussé sans l'humain. 1. Ajouter l'entrée en tête de `CHANGELOG.md` (et `dart/CHANGELOG.md`).
2. Passer `version:` de `dart/pubspec.yaml`. 3. Commit puis `git tag vX.Y.Z`. 4. Les dépendants changent ensuite
leur ref/version : bref et fresque (`go.mod`, `dart/pubspec.yaml`), puis server (`go.mod`) et app (`pubspec.yaml`).

## Renvois
`README.md` (principes, protocole, tests), `CHANGELOG.md`, `dart/README.md`.

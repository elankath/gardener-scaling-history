function parse_flags() {
  while test $# -gt 0; do
    case "$1" in
    --shoot | -d)
      shift
      SHOOT="$1"
      ;;
    --project | -p)
      shift
      PROJECT="$1"
      ;;
    --pod | -p)
      shift
      POD="$1"
      ;;
    esac
    shift
  done
}

function main() {
    parse_flags "$@"
    kubectl cp shoot--$PROJECT--$SHOOT/$POD:/tmp/db/$SHOOT.db ./db-data/$SHOOT.db
}

main "$@"